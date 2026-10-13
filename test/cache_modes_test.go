package test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-acexy/cloud-cache/cachecloud"
	"github.com/golang-acexy/starter-redis/redisstarter"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type integrationValue struct {
	Name       string
	Revision   int `json:"-"`
	Attributes map[string]int
}

func TestAllCacheModes(t *testing.T) {
	if os.Getenv("CLOUD_CACHE_ALL_MODES_CHILD") != "1" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, "-test.run=^TestAllCacheModes$", "-test.timeout=35s")
		cmd.Env = append(os.Environ(), "CLOUD_CACHE_ALL_MODES_CHILD=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("all modes: %v\n%s", err, output)
		}
		return
	}
	startRedis(t)
	service := fmt.Sprintf("cache-modes-%d", time.Now().UnixNano())
	buckets := []cachecloud.BucketName{"memory", "distributed", "redis", "level2"}
	err := cachecloud.Init(cachecloud.Options{ServiceName: service},
		cachecloud.NewMemBucketConfig(buckets[0], time.Second),
		cachecloud.NewDistMemBucketConfig(buckets[1], time.Second),
		cachecloud.NewRedisBucketConfig(buckets[2], time.Second),
		cachecloud.NewLevel2BucketConfig(buckets[3], time.Second, 3*time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, bucket := range buckets {
		t.Run(string(bucket), func(t *testing.T) {
			verifyIntegratedValueTypes(t, bucket)
			verifySingleflightMode(t, bucket)
			key := cachecloud.NewCacheKey("item:%s")
			want := integrationValue{"original", 1, map[string]int{"a": 1, "b": 2}}
			if err := cachecloud.Put(bucket, key, &want, "literal:%s"); err != nil {
				t.Fatal(err)
			}
			want.Name = "mutated"
			want.Attributes["a"] = 99
			var got integrationValue
			if err := cachecloud.Get(bucket, key, &got, "literal:%s"); err != nil {
				t.Fatal(err)
			}
			expected := integrationValue{"original", 1, map[string]int{"a": 1, "b": 2}}
			if !reflect.DeepEqual(got, expected) {
				t.Fatalf("input mutation leaked: %+v", got)
			}
			expected.Revision = 2
			if err := cachecloud.Put(bucket, key, expected, "literal:%s"); err != nil {
				t.Fatal(err)
			}
			got = integrationValue{}
			if err := cachecloud.Get(bucket, key, &got, "literal:%s"); err != nil || !reflect.DeepEqual(got, expected) {
				t.Fatalf("overwrite: %+v %v", got, err)
			}
			if err := cachecloud.Get(bucket, key, &got, "other"); !errors.Is(err, cachecloud.ErrCacheMiss) {
				t.Fatalf("key isolation: %v", err)
			}
			for range 2 {
				if err := cachecloud.Evict(bucket, key, "literal:%s"); err != nil {
					t.Fatalf("idempotent deletion: %v", err)
				}
			}
			calls := 0
			load := func(result *integrationValue) (bool, error) { calls++; *result = expected; return true, nil }
			for range 2 {
				var value integrationValue
				if err := cachecloud.Cacheable(bucket, key, &value, load, "loaded"); err != nil || !reflect.DeepEqual(value, expected) {
					t.Fatalf("cacheable: %+v %v", value, err)
				}
			}
			if calls != 1 {
				t.Fatalf("loader calls=%d", calls)
			}
			if err := cachecloud.Evict(bucket, key, "loaded"); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				var value integrationValue
				if err := cachecloud.Cacheable(bucket, key, &value, func(v *integrationValue) (bool, error) { calls++; *v = expected; return false, nil }, "skip"); err != nil {
					t.Fatal(err)
				}
			}
			if calls != 3 {
				t.Fatal("uncached loader result was stored")
			}
			// Independent keys exercise concurrent public operations without requiring strong consistency.
			var wg sync.WaitGroup
			failures := make(chan error, 8)
			for worker := range 8 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					k := cachecloud.NewCacheKey(fmt.Sprintf("worker:%d", worker))
					for iteration := range 12 {
						if err := cachecloud.Put(bucket, k, iteration); err != nil {
							failures <- err
							return
						}
						var value int
						if err := cachecloud.Get(bucket, k, &value); err != nil || value != iteration {
							failures <- fmt.Errorf("read %d: %d %v", iteration, value, err)
							return
						}
						if err := cachecloud.Evict(bucket, k); err != nil {
							failures <- err
							return
						}
					}
				}()
			}
			wg.Wait()
			close(failures)
			for err := range failures {
				t.Error(err)
			}
		})
	}
	t.Run("level2 rebuild does not publish", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		messages, unsubscribe, err := redisstarter.TopicCmd().SubscribeRepeat(ctx, redisstarter.NewRedisKey(service+":l2-mem-sync-topic"))
		if err != nil {
			t.Fatal(err)
		}
		defer unsubscribe()
		key := cachecloud.NewCacheKey("rebuild-only")
		if err := cachecloud.Put(buckets[3], key, "value"); err != nil {
			t.Fatal(err)
		}
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		found := false
		for !found {
			select {
			case msg := <-messages:
				var event struct {
					CacheKey string `json:"cacheKey"`
				}
				if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
					t.Fatal(err)
				}
				found = event.CacheKey == key.KeyFormat
			case <-timer.C:
				t.Fatal("put event not received")
			}
		}
		// L1 expires while L2 is still valid.
		time.Sleep(1100 * time.Millisecond)
		var got string
		if err := cachecloud.Get(buckets[3], key, &got); err != nil || got != "value" {
			t.Fatalf("rebuild: %q %v", got, err)
		}
		quiet := time.NewTimer(150 * time.Millisecond)
		defer quiet.Stop()
		for {
			select {
			case msg := <-messages:
				var event struct {
					CacheKey string `json:"cacheKey"`
				}
				if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
					t.Fatal(err)
				}
				if event.CacheKey == key.KeyFormat {
					t.Fatal("rebuild published a message")
				}
			case <-quiet.C:
				return
			}
		}
	})
	t.Run("all modes expire", func(t *testing.T) {
		key := cachecloud.NewCacheKey("expiration")
		for _, bucket := range buckets {
			if err := cachecloud.Put(bucket, key, "value"); err != nil {
				t.Fatal(err)
			}
		}
		deadline := time.Now().Add(8 * time.Second)
		remaining := make(map[cachecloud.BucketName]bool)
		for _, bucket := range buckets {
			remaining[bucket] = true
		}
		for len(remaining) > 0 && time.Now().Before(deadline) {
			for bucket := range remaining {
				var value string
				err := cachecloud.Get(bucket, key, &value)
				if errors.Is(err, cachecloud.ErrCacheMiss) {
					delete(remaining, bucket)
				} else if err != nil {
					t.Fatal(err)
				}
			}
			if len(remaining) > 0 {
				time.Sleep(25 * time.Millisecond)
			}
		}
		if len(remaining) > 0 {
			t.Fatalf("entries did not expire: %v", remaining)
		}
	})
}

func readHiddenFieldResult(t *testing.T, bucket cachecloud.BucketName, key cachecloud.CacheKey) string {
	t.Helper()
	var value integrationValue
	err := cachecloud.Get(bucket, key, &value)
	if errors.Is(err, cachecloud.ErrCacheMiss) {
		return "miss"
	}
	if err != nil {
		t.Fatal(err)
	}
	if value.Name != "value" {
		return "invalid"
	}
	return fmt.Sprintf("revision:%d", value.Revision)
}

func verifyIntegratedValueTypes(t *testing.T, bucket cachecloud.BucketName) {
	t.Helper()
	values := []any{
		time.Unix(1700000000, 123).UTC(), decimal.RequireFromString("123.4500"),
		big.NewInt(12345), new(big.Rat).SetFrac64(2, 3), big.NewFloat(1.25),
		json.Number("123.4500"), net.IPv4(127, 0, 0, 1), netip.MustParseAddr("2001:db8::1"),
		uuid.MustParse("123e4567-e89b-12d3-a456-426614174000"),
		sql.NullString{String: "value", Valid: true},
		struct {
			Public  string
			private int
		}{Public: "fallback"},
	}
	for i, value := range values {
		key := cachecloud.NewCacheKey(fmt.Sprintf("integrated-type-%d", i))
		if err := cachecloud.Put(bucket, key, value); err != nil {
			t.Fatalf("put %T: %v", value, err)
		}
		result := reflect.New(reflect.TypeOf(value))
		if err := cachecloud.Get(bucket, key, result.Interface()); err != nil {
			t.Fatalf("get %T: %v", value, err)
		}
		if !reflect.DeepEqual(value, result.Elem().Interface()) {
			t.Fatalf("round trip %T: got %v, want %v", value, result.Elem().Interface(), value)
		}
	}
}

func verifySingleflightMode(t *testing.T, bucket cachecloud.BucketName) {
	t.Helper()
	const workers = 16
	var calls atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]integrationValue, workers)
	errs := make([]error, workers)
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = cachecloud.CacheableSingleflight(bucket, cachecloud.NewCacheKey("flight:%s"), &results[i], func(out *integrationValue) (bool, error) {
				calls.Add(1)
				*out = integrationValue{Name: "shared", Attributes: map[string]int{"a": 1}}
				return true, nil
			}, "literal:%s")
		}()
	}
	close(start)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("singleflight loader calls: %d", calls.Load())
	}
	for i := range workers {
		if errs[i] != nil || results[i].Name != "shared" || results[i].Attributes["a"] != 1 {
			t.Fatalf("singleflight result %d: %v %v", i, results[i], errs[i])
		}
	}
	results[0].Attributes["a"] = 99
	if results[1].Attributes["a"] != 1 {
		t.Fatal("singleflight results share a map")
	}
}
