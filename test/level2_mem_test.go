package test

import (
	"fmt"
	"github.com/acexy/golang-toolkit/logger"
	"github.com/acexy/golang-toolkit/sys"
	"github.com/acexy/golang-toolkit/util/json"
	"github.com/golang-acexy/cloud-cache/cachecloud"
	"github.com/golang-acexy/starter-parent/parent"
	"testing"
	"time"
)

func init() {
	logger.EnableConsole(logger.TraceLevel, false)
}

func TestLevel21(t *testing.T) {
	loader := parent.NewStarterLoader([]parent.Starter{cluster})
	err := loader.Start()
	if err != nil {
		fmt.Printf("%+v\n", err)
		return
	}
	level2Bucket := cachecloud.BucketName("leve2")
	cachecloud.Init(
		cachecloud.Option{
			AutoEnable2LevelCache: true,
		},
		cachecloud.NewCacheConfig(level2Bucket, time.Second*5, cachecloud.BucketTypeMem),
		cachecloud.NewCacheConfig(level2Bucket, time.Hour, cachecloud.BucketTypeRedis),
	)

	cacheKeyTest := cachecloud.CacheKey{KeyFormat: "test"}
	_ = cachecloud.PutCacheValue(level2Bucket, cacheKeyTest, Model{
		Name: "acexy1",
		Sex:  0,
		Age:  38,
	})

	done := make(chan bool)
	go func() {
		for {
			select {
			case <-done:
				break
			default:
				// 获取1秒缓存数据
				var value Model
				_ = cachecloud.GetCacheValue(level2Bucket, cacheKeyTest, &value)
				fmt.Println(json.ToJson(value))
				time.Sleep(time.Second)
			}
		}
	}()

	sys.ShutdownCallback(func() {
		done <- true
	})
}

func TestLevel22(t *testing.T) {
	loader := parent.NewStarterLoader([]parent.Starter{cluster})
	err := loader.Start()
	if err != nil {
		fmt.Printf("%+v\n", err)
		return
	}
	level2Bucket := cachecloud.BucketName("leve2")
	cachecloud.Init(
		cachecloud.Option{
			AutoEnable2LevelCache: true,
		},
		cachecloud.NewCacheConfig(level2Bucket, time.Second*5, cachecloud.BucketTypeMem),
		cachecloud.NewCacheConfig(level2Bucket, time.Hour, cachecloud.BucketTypeRedis),
	)
	cacheKeyTest := cachecloud.CacheKey{KeyFormat: "test"}

	done := make(chan bool)
	go func() {
		for {
			select {
			case <-done:
				break
			default:
				// 获取1秒缓存数据
				var value Model
				_ = cachecloud.GetCacheValue(level2Bucket, cacheKeyTest, &value)
				fmt.Println(json.ToJson(value))
				time.Sleep(time.Second)
			}
		}
	}()

	sys.ShutdownCallback(func() {
		done <- true
	})
}

func TestLevel2Updated(t *testing.T) {
	loader := parent.NewStarterLoader([]parent.Starter{cluster})
	err := loader.Start()
	if err != nil {
		fmt.Printf("%+v\n", err)
		return
	}
	level2Bucket := cachecloud.BucketName("leve2")
	cachecloud.Init(
		cachecloud.Option{
			AutoEnable2LevelCache: true,
		},
		cachecloud.NewCacheConfig(level2Bucket, time.Second*5, cachecloud.BucketTypeMem),
		cachecloud.NewCacheConfig(level2Bucket, time.Hour, cachecloud.BucketTypeRedis),
	)
	cacheKeyTest := cachecloud.CacheKey{KeyFormat: "test"}

	_ = cachecloud.PutCacheValue(level2Bucket, cacheKeyTest, Model{
		Name: "acexy",
		Sex:  1,
		Age:  19,
	})
}

func TestLevel2Deleted(t *testing.T) {
	loader := parent.NewStarterLoader([]parent.Starter{cluster})
	err := loader.Start()
	if err != nil {
		fmt.Printf("%+v\n", err)
		return
	}
	level2Bucket := cachecloud.BucketName("leve2")
	cachecloud.Init(
		cachecloud.Option{
			AutoEnable2LevelCache: true,
		},
		cachecloud.NewCacheConfig(level2Bucket, time.Second*5, cachecloud.BucketTypeMem),
		cachecloud.NewCacheConfig(level2Bucket, time.Hour, cachecloud.BucketTypeRedis),
	)
	cacheKeyTest := cachecloud.CacheKey{KeyFormat: "test"}
	_ = cachecloud.EvictCache(level2Bucket, cacheKeyTest)
}
