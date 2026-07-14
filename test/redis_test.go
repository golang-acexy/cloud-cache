package test

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/golang-acexy/cloud-cache/cachecloud"
	"github.com/golang-acexy/starter-parent/parent"
	"github.com/golang-acexy/starter-redis/redisstarter"
	"github.com/redis/go-redis/v9"
)

func newRedisStarter() *redisstarter.RedisStarter {
	addresses := os.Getenv("CACHE_TEST_REDIS_ADDRS")
	if addresses == "" {
		addresses = ":6379,:6381,:6380"
	}
	return &redisstarter.RedisStarter{
		Config: redisstarter.RedisConfig{
			UniversalOptions: redis.UniversalOptions{
				Addrs:    strings.Split(addresses, ","),
				Password: "tech-acexy",
			},
		},
	}
}

func startRedis(t *testing.T) {
	t.Helper()
	loader := parent.InitStarterLoader([]parent.Starter{newRedisStarter()})
	if err := loader.Start(); err != nil {
		t.Fatalf("start Redis starter: %v", err)
	}
}

func TestRedisBackedCaches(t *testing.T) {
	if os.Getenv("CLOUD_CACHE_REDIS_TEST_CHILD") != "1" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatalf("resolve test binary: %v", err)
		}
		command := exec.Command(binary, "-test.run=^TestRedisBackedCaches$")
		command.Env = append(os.Environ(), "CLOUD_CACHE_REDIS_TEST_CHILD=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("Redis-backed cache child failed: %v\n%s", err, output)
		}
		return
	}

	startRedis(t)

	redisBucket := cachecloud.BucketName("redis-integration")
	distBucket := cachecloud.BucketName("dist-memory-integration")
	level2Bucket := cachecloud.BucketName("level2-integration")
	if err := cachecloud.Init(
		cachecloud.Options{ServiceName: "cloud-cache-integration"},
		cachecloud.NewRedisBucketConfig(redisBucket, time.Minute),
		cachecloud.NewDistMemBucketConfig(distBucket, time.Minute),
		cachecloud.NewLevel2BucketConfig(level2Bucket, time.Minute, time.Minute),
	); err != nil {
		t.Fatalf("init Redis-backed caches: %v", err)
	}

	key := cachecloud.NewCacheKey("model:%s")
	for _, bucketName := range []cachecloud.BucketName{redisBucket, distBucket, level2Bucket} {
		bucketName := bucketName
		t.Run(string(bucketName), func(t *testing.T) {
			want := Model{Name: string(bucketName), Age: 18}
			if err := cachecloud.Put(bucketName, key, want, "1"); err != nil {
				t.Fatalf("put: %v", err)
			}
			var got Model
			if err := cachecloud.Get(bucketName, key, &got, "1"); err != nil {
				t.Fatalf("get: %v", err)
			}
			if got != want {
				t.Fatalf("unexpected value: got %+v, want %+v", got, want)
			}
			if err := cachecloud.Evict(bucketName, key, "1"); err != nil {
				t.Fatalf("evict: %v", err)
			}
			if bucketName == redisBucket {
				if err := cachecloud.Evict(bucketName, key, "1"); err != nil {
					t.Fatalf("Redis eviction should be idempotent: %v", err)
				}
			}
			if err := cachecloud.Get(bucketName, key, &got, "1"); !errors.Is(err, cachecloud.ErrCacheMiss) {
				t.Fatalf("expected cache miss after eviction, got %v", err)
			}
		})
	}
}
