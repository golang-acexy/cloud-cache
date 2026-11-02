package cachecloud

import (
	"context"
	"testing"
	"time"

	"github.com/allegro/bigcache/v3"
	"github.com/golang-acexy/starter-redis/redisstarter"
	"github.com/redis/go-redis/v9"
)

type benchmarkSnapshotTopic struct{}

func (benchmarkSnapshotTopic) Publish(redisstarter.RedisKey, string, ...any) error { return nil }
func (benchmarkSnapshotTopic) SubscribeRetry(context.Context, redisstarter.RedisKey, func(*redis.Message)) {
}

// BenchmarkSingleflightMiss measures the public miss path; Redis and publishing use in-process fakes.
func BenchmarkSingleflightMiss(b *testing.B) {
	for _, mode := range []string{"mem", "dist", "l2"} {
		for _, size := range []int{1024, 100 * 1024} {
			b.Run(mode+"/"+benchmarkSizeName(size), func(b *testing.B) {
				old := loadRuntime()
				oldDist, oldL2 := distMemSyncCmd, level2SyncCmd
				distMemSyncCmd, level2SyncCmd = benchmarkSnapshotTopic{}, benchmarkSnapshotTopic{}
				b.Cleanup(func() {
					runtimeState.Lock()
					runtimeState.instance = old
					runtimeState.Unlock()
					distMemSyncCmd, level2SyncCmd = oldDist, oldL2
				})
				settings := bigcache.Config{Shards: 1, MaxEntriesInWindow: 16, MaxEntrySize: 128, HardMaxCacheSize: 8}
				rt := &cacheRuntime{}
				var err error
				switch mode {
				case "mem":
					rt.memory, err = newMemoryCacheManager([]BucketConfig{NewMemBucketConfig("bench", time.Minute, settings)})
				case "dist":
					rt.distMem, err = newDistMemCacheManager("benchmark", []BucketConfig{NewDistMemBucketConfig("bench", time.Minute, settings)})
				case "l2":
					rt.level2, err = newLevel2CacheManager("benchmark", []BucketConfig{NewLevel2BucketConfig("bench", time.Minute, time.Minute, settings)})
					if err == nil {
						rt.level2.buckets["bench"].redisBucket = &testRedisBucket{}
					}
				}
				if err != nil {
					b.Fatal(err)
				}
				runtimeState.Lock()
				runtimeState.instance = rt
				runtimeState.Unlock()
				value := newCacheValueBenchmarkModel(size)
				loader := func(out *cacheValueBenchmarkModel) (bool, error) { *out = value; return true, nil }
				key := NewCacheKey("item")
				b.ReportAllocs()
				for b.Loop() {
					var result cacheValueBenchmarkModel
					if err := CacheableSingleflight("bench", key, &result, loader); err != nil {
						b.Fatal(err)
					}
					if err := Evict("bench", key); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
