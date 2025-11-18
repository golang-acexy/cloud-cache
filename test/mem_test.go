package test

import (
	"fmt"
	"testing"
	"time"

	"github.com/acexy/golang-toolkit/util/json"
	"github.com/golang-acexy/cloud-cache/cachecloud"
)

type Model struct {
	Name string
	Sex  int
	Age  int
}

func TestMem(t *testing.T) {
	oneSecBucket := cachecloud.BucketName("1s")
	oneHourBucket := cachecloud.BucketName("1h")

	cachecloud.Init(
		cachecloud.Option{}, cachecloud.NewMemCacheConfig(oneSecBucket, time.Second),
		cachecloud.NewMemCacheConfig(oneHourBucket, time.Hour),
	)

	cacheKeyTest := cachecloud.CacheKey{KeyFormat: "test"}
	_ = cachecloud.PutCacheValue(oneSecBucket, cacheKeyTest, Model{
		Name: "acexy",
		Sex:  1,
		Age:  18,
	})
	_ = cachecloud.PutCacheValue(oneHourBucket, cacheKeyTest, Model{
		Name: "acexy1",
		Sex:  11,
		Age:  181,
	})

	// 获取1秒缓存数据
	var value *Model
	_ = cachecloud.GetCacheValue(oneSecBucket, cacheKeyTest, &value)
	fmt.Println(json.ToJson(value))
	_ = cachecloud.GetCacheValue(oneHourBucket, cacheKeyTest, &value)
	fmt.Println(json.ToJson(value))

	// 等待1秒缓存过期后再次获取
	var value1 *Model
	time.Sleep(time.Second * 3)
	fmt.Println("等待3秒后继续获取")
	_ = cachecloud.GetCacheValue(oneSecBucket, cacheKeyTest, &value1)
	fmt.Println(json.ToJson(value1))
	_ = cachecloud.GetCacheValue(oneHourBucket, cacheKeyTest, &value1)
	fmt.Println(json.ToJson(value1))

	// 清除缓存
	fmt.Println("清除缓存后获取")
	_ = cachecloud.EvictCache(oneHourBucket, cacheKeyTest)
	var value2 *Model
	_ = cachecloud.GetCacheValue(oneHourBucket, cacheKeyTest, &value2)
	fmt.Println(json.ToJson(value2))
}

func TestBugFix(t *testing.T) {
	const (
		BucketL225Day cachecloud.BucketName = "l2-25-day" // 二级缓存规则： 25天过期
		BucketMem1Day cachecloud.BucketName = "mem-1-day" // 内存缓存规则： 1天过期
	)

	bucket := []cachecloud.CacheConfig{
		cachecloud.NewLevel2CacheConfig(BucketL225Day, time.Hour*24*5, time.Hour*24*25),
		cachecloud.NewMemCacheConfig(BucketMem1Day, time.Hour*24),
	}
	cachecloud.Init(cachecloud.Option{}, bucket...)

	cachecloud.GetBucket(BucketMem1Day)
}
