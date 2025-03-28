package cachecloud

import (
	"github.com/acexy/golang-toolkit/util/coll"
	"sync"
)

// 设置全局服务名
var serviceNamePrefix string
var once sync.Once

var use2LCache bool
var useDistMemCache bool
var useMemCache bool
var useRedisCache bool

func Init(serviceName string, cacheConfigs ...CacheConfig) {
	once.Do(func() {
		serviceNamePrefix = serviceName
		if len(cacheConfigs) > 0 {
			// 加载内存缓存设置
			memConfigs := coll.SliceFilter(cacheConfigs, func(e CacheConfig) bool {
				return e.typ == BucketTypeMem
			})
			if len(memConfigs) > 0 {
				useMemCache = true
				initMemCacheManager(memConfigs...)
			}
			// 加载分布式内存缓存设置
			distMemConfigs := coll.SliceFilter(cacheConfigs, func(e CacheConfig) bool {
				return e.typ == BucketTypeDistMem
			})
			if len(distMemConfigs) > 0 {
				useDistMemCache = true
				initDistMemCacheManager(distMemConfigs...)
			}
			// 加载redis缓存设置
			redisConfigs := coll.SliceFilter(cacheConfigs, func(e CacheConfig) bool {
				return e.typ == BucketTypeRedis
			})
			if len(redisConfigs) > 0 {
				useRedisCache = true
				initRedisCacheManager(redisConfigs...)
			}
		}
	})
}
