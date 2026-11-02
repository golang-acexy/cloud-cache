package test

import (
	"fmt"
	"github.com/allegro/bigcache/v3"
	"github.com/golang-acexy/starter-parent/parent"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-acexy/cloud-cache/cachecloud"
)

var integrationSnapshotEncodes atomic.Int32

type integrationSnapshotValue struct{ Text string }

// GobEncode records business encoding calls for the snapshot regression.
func (v integrationSnapshotValue) GobEncode() ([]byte, error) {
	integrationSnapshotEncodes.Add(1)
	return []byte(v.Text), nil
}

// GobDecode detects repeated encoding of an already decoded value.
func (v *integrationSnapshotValue) GobDecode(data []byte) error {
	v.Text = "decoded:" + string(data)
	return nil
}

func verifySingleflightSnapshot(t *testing.T, bucket cachecloud.BucketName) {
	t.Helper()
	key := cachecloud.NewCacheKey(fmt.Sprintf("snapshot-%s", bucket))
	defer func() {
		if err := cachecloud.Evict(bucket, key); err != nil {
			t.Error(err)
		}
	}()
	integrationSnapshotEncodes.Store(0)
	var got integrationSnapshotValue
	if err := cachecloud.CacheableSingleflight(bucket, key, &got, func(out *integrationSnapshotValue) (bool, error) { out.Text = "input"; return true, nil }); err != nil {
		t.Fatal(err)
	}
	if got.Text != "decoded:input" || integrationSnapshotEncodes.Load() != 1 {
		t.Fatalf("snapshot: value=%q encodes=%d", got.Text, integrationSnapshotEncodes.Load())
	}
	var cached integrationSnapshotValue
	if err := cachecloud.Get(bucket, key, &cached); err != nil || cached != got {
		t.Fatalf("stored: %+v %v", cached, err)
	}
}

// BenchmarkRedisSingleflightMiss measures built-in bucket misses against the configured Redis instance.
// Run alone with CLOUD_CACHE_SINGLEFLIGHT_BENCH=1 and -count=1; it uses an isolated service namespace.
func BenchmarkRedisSingleflightMiss(b *testing.B) {
	if os.Getenv("CLOUD_CACHE_SINGLEFLIGHT_BENCH") != "1" {
		b.Skip("opt-in Redis benchmark")
	}
	starter := newRedisStarter()
	if err := parent.InitStarterLoader([]parent.Starter{starter}).Start(); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if _, _, err := starter.Stop(5 * time.Second); err != nil {
			b.Error(err)
		}
	})
	service := fmt.Sprintf("flight-bench-%d", time.Now().UnixNano())
	settings := bigcache.Config{Shards: 4, MaxEntriesInWindow: 1024, MaxEntrySize: 1024, HardMaxCacheSize: 16}
	if err := cachecloud.Init(cachecloud.Options{ServiceName: service},
		cachecloud.NewMemBucketConfig("mem", time.Minute, settings),
		cachecloud.NewDistMemBucketConfig("dist", time.Minute, settings),
		cachecloud.NewRedisBucketConfig("redis", time.Minute),
		cachecloud.NewLevel2BucketConfig("l2", time.Minute, time.Minute, settings)); err != nil {
		b.Fatal(err)
	}
	for _, bucket := range []cachecloud.BucketName{"mem", "dist", "redis", "l2"} {
		b.Run(string(bucket), func(b *testing.B) {
			value := strings.Repeat("x", 1024)
			var ids atomic.Int64
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				key := cachecloud.NewCacheKey(fmt.Sprintf("worker-%d", ids.Add(1)))
				defer func() {
					if err := cachecloud.Evict(bucket, key); err != nil {
						b.Error(err)
					}
				}()
				for pb.Next() {
					var got string
					if err := cachecloud.CacheableSingleflight(bucket, key, &got, func(out *string) (bool, error) { *out = value; return true, nil }); err != nil || got != value {
						b.Errorf("snapshot read: %v", err)
						return
					}
					if err := cachecloud.Evict(bucket, key); err != nil {
						b.Error(err)
						return
					}
				}
			})
		})
	}
}
