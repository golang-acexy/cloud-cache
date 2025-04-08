package cachecloud

type cacheManager interface {
	// GetBucket 获取存储桶
	getBucket(bucketName BucketName) CacheBucket
}

func getBucket(name BucketName) CacheBucket {
	cacheBucket := getLevel2Bucket(name)
	if cacheBucket != nil {
		return cacheBucket
	}
	cacheBucket = getDistMemBucket(name)
	if cacheBucket != nil {
		return cacheBucket
	}
	cacheBucket = getMemBucket(name)
	if cacheBucket != nil {
		return cacheBucket
	}
	return getRedisBucket(name)
}
func getBucketByType(name BucketName, typ BucketType) CacheBucket {
	switch typ {
	case BucketTypeMem:
		return getMemBucket(name)
	case BucketTypeRedis:
		return getRedisBucket(name)
	case BucketTypeDistMem:
		return getDistMemBucket(name)
	case BucketTypeLevel2:
		return getLevel2Bucket(name)
	default:
		return nil
	}
}

func getLevel2Bucket(name BucketName) CacheBucket {
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
