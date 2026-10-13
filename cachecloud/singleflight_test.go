package cachecloud

import (
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestCacheableSingleflight(t *testing.T) {
	resetRuntimeForTest(t)
	if err := Init(Options{ServiceName: "flight"}, NewMemBucketConfig("a", time.Minute), NewMemBucketConfig("b", time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"store", "skip", "error", "panic", "goexit"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				const n = 16
				release := make(chan struct{})
				var calls atomic.Int32
				var finished atomic.Int32
				var panics atomic.Int32
				results := make([]map[string][]int, n)
				errs := make([]error, n)
				expected := errors.New("loader error")
				loader := func(out *map[string][]int) (bool, error) {
					calls.Add(1)
					<-release
					switch mode {
					case "error":
						return false, expected
					case "panic":
						panic("loader panic")
					case "goexit":
						runtime.Goexit()
					}
					*out = map[string][]int{"value": {1, 2}}
					return mode == "store", nil
				}
				for i := range n {
					go func() {
						defer finished.Add(1)
						defer func() {
							if recover() != nil {
								panics.Add(1)
							}
						}()
						errs[i] = CacheableSingleflight("a", NewCacheKey(mode+":%s"), &results[i], loader, "literal:%s")
					}()
				}
				synctest.Wait()
				if calls.Load() != 1 {
					t.Fatalf("loaders = %d", calls.Load())
				}
				close(release)
				synctest.Wait()
				if finished.Load() != n {
					t.Fatalf("finished = %d", finished.Load())
				}
				if mode == "panic" {
					if panics.Load() != n {
						t.Fatal("panic did not release all participants")
					}
					return
				}
				if panics.Load() != 0 {
					t.Fatal("unexpected panic")
				}
				if mode == "goexit" {
					return
				}
				for i := range n {
					if mode == "error" {
						if !errors.Is(errs[i], expected) {
							t.Fatal(errs[i])
						}
						continue
					}
					if errs[i] != nil || results[i]["value"][0] != 1 {
						t.Fatalf("result %d: %v %v", i, results[i], errs[i])
					}
				}
				if mode == "error" {
					return
				}
				results[0]["value"][0] = 99
				if results[1]["value"][0] != 1 {
					t.Fatal("shared mutable result")
				}
				var cached map[string][]int
				err := Get("a", NewCacheKey(mode+":%s"), &cached, "literal:%s")
				if mode == "skip" {
					if !errors.Is(err, ErrCacheMiss) {
						t.Fatal(err)
					}
				} else if err != nil || cached["value"][0] != 1 {
					t.Fatal("cached value mutated")
				}
			})
		})
	}
}

func TestCacheableSingleflightIsolationAndValidation(t *testing.T) {
	resetRuntimeForTest(t)
	var out string
	if err := CacheableSingleflight[string]("a", NewCacheKey("k"), nil, nil); !errors.Is(err, ErrResultRequired) {
		t.Fatal(err)
	}
	if err := CacheableSingleflight[string]("a", NewCacheKey("k"), &out, nil); !errors.Is(err, ErrNotInitialized) {
		t.Fatal(err)
	}
	if err := Init(Options{ServiceName: "flight"}, NewMemBucketConfig("a", time.Minute), NewMemBucketConfig("b", time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := CacheableSingleflight[string]("missing", NewCacheKey("k"), &out, nil); !errors.Is(err, ErrBucketNotFound) {
		t.Fatal(err)
	}
	if err := CacheableSingleflight[string]("a", NewCacheKey("k"), &out, nil); !errors.Is(err, ErrCacheMiss) {
		t.Fatal(err)
	}
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		var calls atomic.Int32
		loader := func(out *string) (bool, error) { calls.Add(1); <-release; *out = "ok"; return false, nil }
		for _, tc := range []struct {
			bucket BucketName
			key    string
		}{{"a", "k"}, {"a", "other"}, {"b", "k"}} {
			go func() {
				var value string
				if err := CacheableSingleflight(tc.bucket, NewCacheKey(tc.key), &value, loader); err != nil || value != "ok" {
					t.Error(err)
				}
			}()
		}
		go func() {
			var value int
			err := CacheableSingleflight("a", NewCacheKey("k"), &value, func(out *int) (bool, error) { calls.Add(1); <-release; *out = 42; return false, nil })
			if err != nil || value != 42 {
				t.Error(err)
			}
		}()
		synctest.Wait()
		if calls.Load() != 4 {
			t.Fatalf("independent flights = %d", calls.Load())
		}
		close(release)
	})
	if err := Put("a", NewCacheKey("hit"), "cached"); err != nil {
		t.Fatal(err)
	}
	if err := CacheableSingleflight("a", NewCacheKey("hit"), &out, func(*string) (bool, error) { t.Fatal("loader on hit"); return false, nil }); err != nil || out != "cached" {
		t.Fatal(err)
	}
	var invalid chan int
	if err := CacheableSingleflight("a", NewCacheKey("unsupported"), &invalid, func(out *chan int) (bool, error) { *out = make(chan int); return false, nil }); err == nil {
		t.Fatal("unsupported snapshot accepted")
	}
}

func TestCacheableSingleflightWriteAndReadErrors(t *testing.T) {
	resetRuntimeForTest(t)
	topic := useTestTopics(t)
	expected := errors.New("publication failed")
	topic.err = expected
	manager, err := newDistMemCacheManager("flight-errors", []BucketConfig{NewDistMemBucketConfig("dist", time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	runtimeState.Lock()
	runtimeState.instance = &cacheRuntime{distMem: manager}
	runtimeState.Unlock()
	var value string
	err = CacheableSingleflight("dist", NewCacheKey("key"), &value, func(out *string) (bool, error) { *out = "loaded"; return true, nil })
	if !errors.Is(err, expected) || value != "loaded" {
		t.Fatalf("write failure lost result: %q %v", value, err)
	}
	if len(topic.events) != 1 {
		t.Fatal("unexpected publication count")
	}
	var wrong int
	err = CacheableSingleflight("dist", NewCacheKey("key"), &wrong, func(*int) (bool, error) { t.Fatal("codec error must not invoke loader"); return false, nil })
	if err == nil || errors.Is(err, ErrCacheMiss) {
		t.Fatalf("expected codec error, got %v", err)
	}
}
