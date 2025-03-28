package cachecloud

import "github.com/acexy/golang-toolkit/logger"

type cacheManager interface {
	// GetBucket 获取存储桶
	getBucket(bucketName BucketName) CacheBucket
}

func getBucket(name BucketName) CacheBucket {
	cacheBucket := get2LBucket(name)
	if cacheBucket != nil {
		logger.Logrus().Traceln(name, "使用二级缓存管理器处理")
		return cacheBucket
	}
	cacheBucket = getDistMemBucket(name)
	if cacheBucket != nil {
		logger.Logrus().Traceln(name, "使用分布式内存缓存管理器处理")
		return cacheBucket
	}
	cacheBucket = getMemBucket(name)
	if cacheBucket != nil {
		logger.Logrus().Traceln(name, "使用独立内存缓存管理器处理")
		return cacheBucket
	}
	logger.Logrus().Traceln(name, "使用Redis缓存管理器处理")
	return getRedisBucket(name)
}
func getBucketByType(name BucketName, typ BucketType) CacheBucket {
	switch typ {
	case BucketType2L:
		return get2LBucket(name)
	case BucketTypeDistMem:
		return getDistMemBucket(name)
	case BucketTypeMem:
		return getMemBucket(name)
	case BucketTypeRedis:
		return getRedisBucket(name)
	}
	return nil
}

func get2LBucket(name BucketName) CacheBucket {
	if use2LCache {
		return level2Cache.getBucket(name)
	}
	return nil
}

func getDistMemBucket(name BucketName) CacheBucket {
	if useDistMemCache {
		return distMemCache.getBucket(name)
	}
	return nil
}

func getMemBucket(name BucketName) CacheBucket {
	if useMemCache {
		return memCache.getBucket(name)
	}
	return nil
}

func getRedisBucket(name BucketName) CacheBucket {
	if useRedisCache {
		return redisCache.getBucket(name)
	}
	return nil
}
