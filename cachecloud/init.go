package cachecloud

import (
	"github.com/acexy/golang-toolkit/logger"
	"github.com/acexy/golang-toolkit/util/coll"
	"strings"
	"sync"
)

// 设置全局服务名
var serviceNamePrefix string
var once sync.Once

var use2LCache bool
var useDistMemCache bool
var useMemCache bool
var useRedisCache bool

func Init(option Option, cacheConfigs ...CacheConfig) {
	once.Do(func() {
		serviceNamePrefix = option.ServiceName
		if len(cacheConfigs) > 0 {
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
			memConfigs := coll.SliceFilter(cacheConfigs, func(e CacheConfig) bool {
				return e.typ == BucketTypeMem
			})
			if option.AutoEnable2LevelCache && len(memConfigs) > 0 && len(redisConfigs) > 0 {
				leve2Configs := coll.SliceIntersection(memConfigs, coll.SliceFilter(redisConfigs, func(e CacheConfig) bool {
					return e.typ == BucketTypeRedis
				}), func(part1, part2 CacheConfig) bool {
					return part1.bucketName == part2.bucketName
				})

				if len(leve2Configs) > 0 {
					leve2MemConfigs := coll.SliceFilter(memConfigs, func(e CacheConfig) bool {
						return coll.SliceContains(leve2Configs, e, func(c1 CacheConfig, c2 CacheConfig) bool {
							return c1.bucketName == c2.bucketName
						})
					})
					use2LCache = true
					logger.Logrus().Debugln("已启用自动二级缓存管理，发现匹配存储桶", strings.Join(coll.SliceCollect(leve2MemConfigs, func(e CacheConfig) string {
						return string(e.bucketName)
					}), ","))
					initSecondLevelCacheManager(leve2MemConfigs)
					// 移除已被二级缓存管理的内存缓存
					memConfigs = coll.SliceComplement(memConfigs, leve2MemConfigs, func(part1, part2 CacheConfig) bool {
						return part1.bucketName == part2.bucketName
					})
				}
			}
			// 加载内存缓存设置
			if len(memConfigs) > 0 {
				useMemCache = true
				initMemCacheManager(memConfigs...)
			}
		}
	})
}
