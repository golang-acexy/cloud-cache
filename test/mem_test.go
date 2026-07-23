package test

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-acexy/cloud-cache/cachecloud"
)

type Model struct {
	Name string
	Sex  int
	Age  int
}

func TestMemoryCacheAndCacheable(t *testing.T) {
	bucketName := cachecloud.BucketName("memory")
	if err := cachecloud.Init(
		cachecloud.Options{ServiceName: "memory-test"},
		cachecloud.NewMemBucketConfig(bucketName, time.Minute),
	); err != nil {
		t.Fatalf("init memory cache: %v", err)
	}

	key := cachecloud.NewCacheKey("model:%d")
	if cachecloud.GetBucket(bucketName) == nil {
		t.Fatal("expected configured memory bucket")
	}
	if cachecloud.GetBucketByType(bucketName, cachecloud.BucketTypeMem) == nil {
		t.Fatal("expected memory bucket by type")
	}
	if cachecloud.GetBucketByType(bucketName, cachecloud.BucketTypeRedis) != nil {
		t.Fatal("memory bucket must not resolve as Redis bucket")
	}
	if err := cachecloud.Get("unknown", key, new(Model)); !errors.Is(err, cachecloud.ErrBucketNotFound) {
		t.Fatalf("expected unknown bucket error, got %v", err)
	}
	var nilResult *Model
	if err := cachecloud.Get(bucketName, key, nilResult, 1); !errors.Is(err, cachecloud.ErrResultRequired) {
		t.Fatalf("expected result required error, got %v", err)
	}

	want := Model{Name: "acexy", Sex: 1, Age: 18}
	if err := cachecloud.Put(bucketName, key, want, 1); err != nil {
		t.Fatalf("put memory cache: %v", err)
	}
	var got Model
	if err := cachecloud.Get(bucketName, key, &got, 1); err != nil {
		t.Fatalf("get memory cache: %v", err)
	}
	if got != want {
		t.Fatalf("unexpected memory value: got %+v, want %+v", got, want)
	}

	if err := cachecloud.Evict(bucketName, key, 1); err != nil {
		t.Fatalf("evict memory cache: %v", err)
	}
	if err := cachecloud.Get(bucketName, key, &got, 1); !errors.Is(err, cachecloud.ErrCacheMiss) {
		t.Fatalf("expected cache miss after eviction, got %v", err)
	}

	loaderCalls := 0
	var rebuilt Model
	err := cachecloud.Cacheable(bucketName, key, &rebuilt, func(result *Model) (bool, error) {
		loaderCalls++
		*result = Model{Name: "rebuilt", Age: 20}
		return true, nil
	}, 3)
	if err != nil {
		t.Fatalf("rebuild cache: %v", err)
	}
	if rebuilt.Name != "rebuilt" || loaderCalls != 1 {
		t.Fatalf("unexpected rebuilt value or loader count: value=%+v calls=%d", rebuilt, loaderCalls)
	}

	var cached Model
	err = cachecloud.Cacheable(bucketName, key, &cached, func(result *Model) (bool, error) {
		loaderCalls++
		return false, errors.New("loader must not be called on hit")
	}, 3)
	if err != nil {
		t.Fatalf("read cacheable hit: %v", err)
	}
	if cached != rebuilt || loaderCalls != 1 {
		t.Fatalf("cacheable hit did not reuse cache: value=%+v calls=%d", cached, loaderCalls)
	}
}
