package test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-acexy/cloud-cache/cachecloud"
	"github.com/golang-acexy/starter-redis/redisstarter"
)

func TestNilAndZeroCacheModes(t *testing.T) {
	const childEnv = "CLOUD_CACHE_NIL_MODES_CHILD"
	if os.Getenv(childEnv) != "1" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, "-test.run=^TestNilAndZeroCacheModes$", "-test.v", "-test.timeout=35s")
		cmd.Env = append(os.Environ(), childEnv+"=1")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("nil/zero integration: %v\n%s", err, output)
		}
		t.Logf("isolated integration results:\n%s", output)
		return
	}

	startRedis(t)
	service := fmt.Sprintf("nil-modes-%d", time.Now().UnixNano())
	buckets := []cachecloud.BucketName{"memory", "distributed", "level2", "redis"}
	if err := cachecloud.Init(cachecloud.Options{ServiceName: service},
		cachecloud.NewMemBucketConfig(buckets[0], time.Minute),
		cachecloud.NewDistMemBucketConfig(buckets[1], time.Minute),
		cachecloud.NewLevel2BucketConfig(buckets[2], time.Second, time.Minute),
		cachecloud.NewRedisBucketConfig(buckets[3], time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	for _, bucket := range buckets {
		t.Run(string(bucket), func(t *testing.T) {
			verifyNilOrZeroValue[any](t, bucket, "nil_interface", nil, func() any { return "stale" })
			verifyNilOrZeroValue[*Model](t, bucket, "nil_pointer", nil, func() *Model { return &Model{Name: "stale"} })
			verifyNilOrZeroValue[[]int](t, bucket, "nil_slice", nil, func() []int { return []int{1} })
			verifyNilOrZeroValue[map[string]int](t, bucket, "nil_map", nil, func() map[string]int { return map[string]int{"stale": 1} })
			verifyNilOrZeroValue(t, bucket, "zero_int", 0, func() int { return 42 })
			verifyNilOrZeroValue(t, bucket, "empty_string", "", func() string { return "stale" })
			verifyNilOrZeroValue(t, bucket, "false", false, func() bool { return true })
			// Gob omits zero struct fields, so non-nil structs use fresh destinations as documented.
			verifyNilOrZeroValue(t, bucket, "zero_struct", Model{}, func() Model { return Model{} })
		})
	}

	t.Run("redis stores an existing empty value", func(t *testing.T) {
		key := cachecloud.NewCacheKey("nil-wire")
		if err := cachecloud.Put("redis", key, nil); err != nil {
			t.Fatal(err)
		}
		defer evictNilTestKey(t, "redis", key)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		client := redisstarter.RawRedisClient()
		rawKey := service + ":redis:" + key.KeyFormat
		payload, err := client.Get(ctx, rawKey).Bytes()
		if err != nil || len(payload) != 0 {
			t.Fatalf("nil must be an existing empty Redis value: length=%d err=%v", len(payload), err)
		}
		if ttl, err := client.PTTL(ctx, rawKey).Result(); err != nil || ttl <= 0 {
			t.Fatalf("nil entry must retain TTL: ttl=%v err=%v", ttl, err)
		}
	})

	t.Run("level2 nil rebuild and cached reuse", func(t *testing.T) {
		key := cachecloud.NewCacheKey("nil-rebuild")
		defer evictNilTestKey(t, "level2", key)
		calls := 0
		loader := func(out **Model) (bool, error) {
			calls++
			*out = nil
			return true, nil
		}
		var result *Model
		if err := cachecloud.Cacheable("level2", key, &result, loader); err != nil {
			t.Fatal(err)
		}
		// The logical L1 deadline expires while the Redis envelope remains valid.
		time.Sleep(1100 * time.Millisecond)
		result = &Model{Name: "stale"}
		if err := cachecloud.Cacheable("level2", key, &result, loader); err != nil || result != nil || calls != 1 {
			t.Fatalf("nil rebuild must bypass loader: result=%v calls=%d err=%v", result, calls, err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		// Removing only L2 proves the preceding read repopulated L1 with the nil envelope.
		if err := redisstarter.RawRedisClient().Del(ctx, service+":l2:level2:"+key.KeyFormat).Err(); err != nil {
			t.Fatal(err)
		}
		result = &Model{Name: "stale"}
		if err := cachecloud.Cacheable("level2", key, &result, loader); err != nil || result != nil || calls != 1 {
			t.Fatalf("rebuilt nil must hit L1: result=%v calls=%d err=%v", result, calls, err)
		}
	})
}

func verifyNilOrZeroValue[T any](t *testing.T, bucket cachecloud.BucketName, name string, want T, destination func() T) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		t.Run("put_get_overwrite_evict", func(t *testing.T) {
			key := cachecloud.NewCacheKey(name + ":put")
			defer evictNilTestKey(t, bucket, key)
			for _, input := range []any{want, &want} {
				if err := cachecloud.Put(bucket, key, destination()); err != nil {
					t.Fatal(err)
				}
				// Overwrite a previous non-nil value with the empty result under test.
				if err := cachecloud.Put(bucket, key, input); err != nil {
					t.Fatal(err)
				}
				result := destination()
				if err := cachecloud.Get(bucket, key, &result); err != nil || !reflect.DeepEqual(result, want) {
					t.Fatalf("empty hit: got=%#v want=%#v err=%v", result, want, err)
				}
			}
			if err := cachecloud.Evict(bucket, key); err != nil {
				t.Fatal(err)
			}
			result := destination()
			if err := cachecloud.Get(bucket, key, &result); !errors.Is(err, cachecloud.ErrCacheMiss) {
				t.Fatalf("evicted empty result must miss: %v", err)
			}
		})
		t.Run("cacheable_loads_once", func(t *testing.T) {
			key := cachecloud.NewCacheKey(name + ":cacheable")
			defer evictNilTestKey(t, bucket, key)
			calls := 0
			loader := func(out *T) (bool, error) { calls++; *out = want; return true, nil }
			for range 3 {
				result := destination()
				if err := cachecloud.Cacheable(bucket, key, &result, loader); err != nil || !reflect.DeepEqual(result, want) {
					t.Fatalf("cacheable result=%#v err=%v", result, err)
				}
			}
			if calls != 1 {
				t.Fatalf("cached empty result must avoid repeated source reads: calls=%d", calls)
			}
			if err := cachecloud.Evict(bucket, key); err != nil {
				t.Fatal(err)
			}
			result := destination()
			if err := cachecloud.Cacheable(bucket, key, &result, loader); err != nil || !reflect.DeepEqual(result, want) || calls != 2 {
				t.Fatalf("eviction must allow reloading: result=%#v calls=%d err=%v", result, calls, err)
			}
		})
		t.Run("false_skips_storage", func(t *testing.T) {
			key := cachecloud.NewCacheKey(name + ":skip")
			defer evictNilTestKey(t, bucket, key)
			calls := 0
			for range 2 {
				result := destination()
				err := cachecloud.Cacheable(bucket, key, &result, func(out *T) (bool, error) { calls++; *out = want; return false, nil })
				if err != nil || !reflect.DeepEqual(result, want) {
					t.Fatalf("uncached result=%#v err=%v", result, err)
				}
			}
			result := destination()
			if err := cachecloud.Get(bucket, key, &result); !errors.Is(err, cachecloud.ErrCacheMiss) || calls != 2 {
				t.Fatalf("uncached result must miss: calls=%d err=%v", calls, err)
			}
		})
		t.Run("singleflight_concurrent_loads_once", func(t *testing.T) {
			key := cachecloud.NewCacheKey(name + ":flight")
			defer evictNilTestKey(t, bucket, key)
			const workers = 16
			var calls atomic.Int32
			var wg sync.WaitGroup
			start := make(chan struct{})
			results := make([]T, workers)
			errs := make([]error, workers)
			for i := range workers {
				results[i] = destination()
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					errs[i] = cachecloud.CacheableSingleflight(bucket, key, &results[i], func(out *T) (bool, error) {
						calls.Add(1)
						*out = want
						return true, nil
					})
				}()
			}
			close(start)
			wg.Wait()
			for i := range workers {
				if errs[i] != nil || !reflect.DeepEqual(results[i], want) {
					t.Fatalf("worker %d: got=%#v want=%#v err=%v", i, results[i], want, errs[i])
				}
			}
			if calls.Load() != 1 {
				t.Fatalf("concurrent empty result must load once: calls=%d", calls.Load())
			}
		})
	})
}

func evictNilTestKey(t *testing.T, bucket cachecloud.BucketName, key cachecloud.CacheKey) {
	t.Helper()
	if err := cachecloud.Evict(bucket, key); err != nil {
		t.Errorf("clean up %s/%s: %v", bucket, key.KeyFormat, err)
	}
}
