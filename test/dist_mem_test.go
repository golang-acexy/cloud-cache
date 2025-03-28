package test

import (
	"fmt"
	"github.com/acexy/golang-toolkit/sys"
	"github.com/acexy/golang-toolkit/util/json"
	"github.com/golang-acexy/cloud-cache/cachecloud"
	"github.com/golang-acexy/starter-parent/parent"
	"testing"
	"time"
)

func TestNode1(t *testing.T) {
	loader := parent.NewStarterLoader([]parent.Starter{cluster})
	err := loader.Start()
	if err != nil {
		fmt.Printf("%+v\n", err)
		return
	}
	oneHourBucket := cachecloud.BucketName("1h")
	cachecloud.Init(
		"",
		cachecloud.NewCacheConfig(oneHourBucket, time.Hour, cachecloud.BucketTypeDistMem),
	)
	cacheKeyTest := cachecloud.CacheKey{KeyFormat: "test"}
	_ = cachecloud.PutCacheValue(oneHourBucket, cacheKeyTest, Model{
		Name: "acexy",
		Sex:  1,
		Age:  18,
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
				_ = cachecloud.GetCacheValue(oneHourBucket, cacheKeyTest, &value)
				fmt.Println(json.ToJson(value))
				time.Sleep(time.Second)
			}
		}
	}()

	sys.ShutdownCallback(func() {
		done <- true
	})
}

func TestNode2(t *testing.T) {
	loader := parent.NewStarterLoader([]parent.Starter{cluster})
	err := loader.Start()
	if err != nil {
		fmt.Printf("%+v\n", err)
		return
	}
	oneHourBucket := cachecloud.BucketName("1h")
	cachecloud.Init(
		cachecloud.NewCacheConfig(oneHourBucket, time.Hour, cachecloud.BucketTypeDistMem),
	)
	cacheKeyTest := cachecloud.CacheKey{KeyFormat: "test"}
	_ = cachecloud.PutCacheValue(oneHourBucket, cacheKeyTest, Model{
		Name: "acexy",
		Sex:  1,
		Age:  18,
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
				_ = cachecloud.GetCacheValue(oneHourBucket, cacheKeyTest, &value)
				fmt.Println(json.ToJson(value))
				time.Sleep(time.Second)
			}
		}
	}()

	sys.ShutdownCallback(func() {
		done <- true
	})
}

func TestNodeUpdated(t *testing.T) {
	loader := parent.NewStarterLoader([]parent.Starter{cluster})
	err := loader.Start()
	if err != nil {
		fmt.Printf("%+v\n", err)
		return
	}
	oneHourBucket := cachecloud.BucketName("1h")
	cachecloud.Init(
		"",
		cachecloud.NewCacheConfig(oneHourBucket, time.Hour, cachecloud.BucketTypeDistMem),
	)

	cacheKeyTest := cachecloud.CacheKey{KeyFormat: "test"}
	_ = cachecloud.PutCacheValue(oneHourBucket, cacheKeyTest, Model{
		Name: "acexy",
		Sex:  1,
		Age:  19,
	})
}

func TestNodeDeleted(t *testing.T) {
	loader := parent.NewStarterLoader([]parent.Starter{cluster})
	err := loader.Start()
	if err != nil {
		fmt.Printf("%+v\n", err)
		return
	}
	oneHourBucket := cachecloud.BucketName("1h")
	cachecloud.Init(
		"",
		cachecloud.NewCacheConfig(oneHourBucket, time.Hour, cachecloud.BucketTypeDistMem),
	)

	cacheKeyTest := cachecloud.CacheKey{KeyFormat: "test"}
	_ = cachecloud.EvictCache(oneHourBucket, cacheKeyTest)
}
