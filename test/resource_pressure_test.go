package test

import (
	"bytes"
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-acexy/cloud-cache/cachecloud"
	"github.com/golang-acexy/starter-parent/parent"
)

// TestCacheResourcePressure runs a bounded Redis-backed workload in its own process.
// Select this test alone with CLOUD_CACHE_PRESSURE=1; each run uses a unique namespace.
func TestCacheResourcePressure(t *testing.T) {
	if os.Getenv("CLOUD_CACHE_PRESSURE") != "1" {
		t.Skip("opt-in Redis pressure test")
	}
	starter := newRedisStarter()
	loader := parent.InitStarterLoader([]parent.Starter{starter})
	if err := loader.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	defer func() {
		if !stopped {
			_, _, _ = starter.Stop(5 * time.Second)
		}
	}()
	service := fmt.Sprintf("cache-pressure-%d", time.Now().UnixNano())
	buckets := []cachecloud.BucketName{"mem", "dist", "redis", "l2"}
	if err := cachecloud.Init(cachecloud.Options{ServiceName: service},
		cachecloud.NewMemBucketConfig(buckets[0], time.Second),
		cachecloud.NewDistMemBucketConfig(buckets[1], time.Second),
		cachecloud.NewRedisBucketConfig(buckets[2], time.Minute),
		cachecloud.NewLevel2BucketConfig(buckets[3], time.Second, time.Minute)); err != nil {
		t.Fatal(err)
	}
	snapshot := func(label string) {
		runtime.GC()
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		var stack bytes.Buffer
		_ = pprof.Lookup("goroutine").WriteTo(&stack, 2)
		t.Logf("%s heap=%d objects=%d goroutines=%d subscriptionLoops=%d", label, m.HeapAlloc, m.HeapObjects, runtime.NumGoroutine(), strings.Count(stack.String(), "cmdTopic).SubscribeRetry.func1"))
	}
	snapshot("initialized")
	const workers = 16
	const iterations = 128
	value := strings.Repeat("x", 1024)
	for round := 0; round < 12; round++ {
		for _, bucket := range buckets {
			var wg sync.WaitGroup
			failures := make(chan error, workers*2)
			samples := make([]time.Duration, workers*iterations)
			start := time.Now()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			for worker := 0; worker < workers; worker++ {
				wg.Add(1)
				go func(w int) {
					defer wg.Done()
					key := cachecloud.NewCacheKey(fmt.Sprintf("worker-%d", w))
					defer func() {
						if err := cachecloud.Evict(bucket, key); err != nil {
							failures <- err
						}
					}()
					for i := 0; i < iterations; i++ {
						at := time.Now()
						if err := cachecloud.Put(bucket, key, value); err != nil {
							failures <- err
							return
						}
						var got string
						if err := cachecloud.Get(bucket, key, &got); err != nil || got != value {
							failures <- fmt.Errorf("read mismatch: %v", err)
							return
						}
						if err := cachecloud.Evict(bucket, key); err != nil {
							failures <- err
							return
						}
						samples[w*iterations+i] = time.Since(at)
					}
				}(worker)
			}
			wg.Wait()
			close(failures)
			for err := range failures {
				t.Error(err)
			}
			if t.Failed() {
				return
			}
			elapsed := time.Since(start)
			runtime.ReadMemStats(&after)
			sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
			t.Logf("round=%d bucket=%s cycles=%d ops/s=%.0f p50=%s p95=%s p99=%s allocBytes/cycle=%d", round, bucket, len(samples), float64(len(samples)*3)/elapsed.Seconds(), samples[len(samples)/2], samples[len(samples)*95/100], samples[len(samples)*99/100], (after.TotalAlloc-before.TotalAlloc)/uint64(len(samples)))
		}
		time.Sleep(time.Second)
		snapshot(fmt.Sprintf("after round %d", round))
	}
	// Compare retained capacity after equal large-payload bursts, not RSS alone.
	for round := 0; round < 6; round++ {
		large := strings.Repeat("y", 256<<10)
		for i := 0; i < 64; i++ {
			if err := cachecloud.Put("mem", cachecloud.NewCacheKey(fmt.Sprintf("burst-%d", i)), large); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < 64; i++ {
			if err := cachecloud.Evict("mem", cachecloud.NewCacheKey(fmt.Sprintf("burst-%d", i))); err != nil {
				t.Fatal(err)
			}
		}
		time.Sleep(2100 * time.Millisecond)
		snapshot(fmt.Sprintf("after burst %d eviction", round))
	}
	time.Sleep(6 * time.Second)
	snapshot("after idle cleanup")
	gracefully, didStop, err := starter.Stop(5 * time.Second)
	stopped = didStop
	if err != nil || !gracefully || !didStop {
		t.Fatalf("Redis stop: %t %t %v", gracefully, didStop, err)
	}
	// Let the retry loops observe the closed subscriber channels.
	time.Sleep(100 * time.Millisecond)
	snapshot("after Redis stop")
}
