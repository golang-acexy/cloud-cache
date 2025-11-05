package test

import (
	"fmt"
	"testing"
	"time"

	"github.com/acexy/golang-toolkit/logger"
	"github.com/acexy/golang-toolkit/math/random"
	"github.com/acexy/golang-toolkit/sys"
	"github.com/golang-acexy/cloud-cache/cachecloud"
)

func TestCacheable(t *testing.T) {
	fiveSecBucket := cachecloud.BucketName("5s")

	cachecloud.Init(
		cachecloud.Option{},
		cachecloud.NewMemCacheConfig(fiveSecBucket, time.Second*5),
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
				var result int
				err := cachecloud.Cacheable[int](fiveSecBucket, cacheKeyTest, &result, func() (*int, bool) {
					newValue := random.RandInt(10)
					logger.Logrus().Debugln("获取新值", newValue)
					return &newValue, true
				})
				if err != nil {
					return
				}
				fmt.Println(result)
				time.Sleep(time.Second)
			}
		}
	}()

	sys.ShutdownCallback(func() {
		done <- true
	})
}
