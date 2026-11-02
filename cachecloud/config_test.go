package cachecloud

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/acexy/golang-toolkit/caching"
	"github.com/allegro/bigcache/v3"
)

func TestBigCacheConfigOwnershipAndValidation(t *testing.T) {
	custom := bigcache.DefaultConfig(time.Hour)
	custom.Shards = 1
	custom.CleanWindow = 0
	custom.MaxEntriesInWindow = 16
	custom.MaxEntrySize = 64
	custom.HardMaxCacheSize = 1
	configs := []BucketConfig{
		NewMemBucketConfig("mem", time.Minute, custom),
		NewDistMemBucketConfig("dist", time.Minute, custom),
		NewLevel2BucketConfig("l2", time.Minute, 2*time.Minute, custom),
	}
	custom.Shards = 3
	for _, config := range configs {
		if config.bigCacheConfig.Shards != 1 || config.bigCacheConfig.LifeWindow != time.Hour {
			t.Fatal("constructor did not snapshot config")
		}
		if err := validateConfig(Options{ServiceName: "custom"}, []BucketConfig{config}); err != nil {
			t.Fatal(err)
		}
		if _, err := newMemoryBucket(config); err != nil {
			t.Fatal(err)
		}
		if config.bigCacheConfig.LifeWindow != time.Hour {
			t.Fatal("construction mutated stored config")
		}
	}
	if NewMemBucketConfig("default", time.Minute).bigCacheConfig != nil {
		t.Fatal("default configuration changed")
	}
	good := *configs[0].bigCacheConfig
	if err := validateConfig(Options{ServiceName: "custom"}, []BucketConfig{NewMemBucketConfig("first", time.Minute, good, custom)}); err != nil {
		t.Fatal("later config must be ignored", err)
	}
	for _, mutate := range []func(*bigcache.Config){
		func(c *bigcache.Config) { c.Shards = 0 },
		func(c *bigcache.Config) { c.Shards = 3 },
		func(c *bigcache.Config) { c.MaxEntrySize = -1 },
		func(c *bigcache.Config) { c.MaxEntriesInWindow = -1 },
		func(c *bigcache.Config) { c.HardMaxCacheSize = -1 },
	} {
		c := good
		mutate(&c)
		if err := validateConfig(Options{ServiceName: "custom"}, []BucketConfig{NewMemBucketConfig("invalid", time.Minute, c)}); !errors.Is(err, ErrInvalidBigCacheConfig) {
			t.Fatalf("invalid config: %v", err)
		}
	}
	good.CleanWindow = time.Second
	if err := validateConfig(Options{ServiceName: "custom"}, []BucketConfig{NewMemBucketConfig("short", time.Millisecond, good)}); !errors.Is(err, ErrInvalidBigCacheConfig) {
		t.Fatalf("short TTL: %v", err)
	}
	good.LifeWindow = time.Millisecond
	if err := validateConfig(Options{ServiceName: "custom"}, []BucketConfig{NewMemBucketConfig("override", time.Minute, good)}); err != nil {
		t.Fatal("LifeWindow must use bucket TTL", err)
	}
}

func TestCustomBigCacheSettingsReachAllMemoryModes(t *testing.T) {
	for _, mode := range []BucketType{BucketTypeMem, BucketTypeDistMem, BucketTypeLevel2} {
		t.Run(string(mode), func(t *testing.T) {
			var removed atomic.Int32
			c := bigcache.DefaultConfig(time.Hour)
			c.Shards = 1
			c.CleanWindow = 0
			c.MaxEntriesInWindow = 16
			c.MaxEntrySize = 64
			c.HardMaxCacheSize = 1
			c.OnRemove = func(string, []byte) { removed.Add(1) }
			config := NewMemBucketConfig("custom", time.Minute, c)
			var manager *caching.CacheManager
			var name caching.BucketName
			switch mode {
			case BucketTypeMem:
				m, err := newMemoryCacheManager([]BucketConfig{config})
				if err != nil {
					t.Fatal(err)
				}
				manager = m.buckets["custom"].manager
				name = m.buckets["custom"].bucketName
			case BucketTypeDistMem:
				config = NewDistMemBucketConfig("custom", time.Minute, c)
				m, err := newDistMemCacheManager("custom-test", []BucketConfig{config})
				if err != nil {
					t.Fatal(err)
				}
				manager = m.manager
				name = m.buckets["custom"].managerBucketName
			case BucketTypeLevel2:
				config = NewLevel2BucketConfig("custom", time.Minute, time.Hour, c)
				m, err := newLevel2CacheManager("custom-test", []BucketConfig{config})
				if err != nil {
					t.Fatal(err)
				}
				if m.buckets["custom"].redisExpiration != time.Hour {
					t.Fatal("Redis TTL changed")
				}
				manager = m.manager
				name = m.buckets["custom"].memoryBucketName
			}
			key := caching.NewCacheKey("value")
			if err := manager.PutBytes(name, key, []byte("value")); err != nil {
				t.Fatal(err)
			}
			if err := manager.Evict(name, key); err != nil {
				t.Fatal(err)
			}
			if removed.Load() != 1 {
				t.Fatal("custom callback not used")
			}
			if err := manager.PutBytes(name, key, []byte(strings.Repeat("x", 2<<20))); err == nil {
				t.Fatal("custom hard capacity was ignored")
			}
		})
	}
}

func TestCustomBigCacheUsesBucketTTL(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := bigcache.DefaultConfig(time.Hour)
		c.Shards = 1
		c.CleanWindow = 0
		c.MaxEntriesInWindow = 16
		c.MaxEntrySize = 64
		bucket, err := newMemoryBucket(NewMemBucketConfig("ttl", time.Second, c))
		if err != nil {
			t.Fatal(err)
		}
		first := caching.NewCacheKey("first")
		if err := bucket.PutBytes(first, []byte("value")); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Second)
		if err := bucket.PutBytes(caching.NewCacheKey("second"), []byte("value")); err != nil {
			t.Fatal(err)
		}
		if _, err := bucket.GetBytes(first); err == nil {
			t.Fatal("custom LifeWindow overrode bucket TTL")
		}
	})
}
