package cachecloud

func getBucket(name BucketName) CacheBucket {
	runtime := loadRuntime()
	if runtime == nil {
		return nil
	}
	return getBucketFromRuntime(runtime, name)
}

func getBucketFromRuntime(runtime *cacheRuntime, name BucketName) CacheBucket {
	if runtime.level2 != nil {
		if bucket := runtime.level2.getBucket(name); bucket != nil {
			return bucket
		}
	}
	if runtime.distMem != nil {
		if bucket := runtime.distMem.getBucket(name); bucket != nil {
			return bucket
		}
	}
	if runtime.memory != nil {
		if bucket := runtime.memory.getBucket(name); bucket != nil {
			return bucket
		}
	}
	if runtime.redis != nil {
		if bucket := runtime.redis.getBucket(name); bucket != nil {
			return bucket
		}
	}
	return nil
}

func getBucketByType(name BucketName, bucketType BucketType) CacheBucket {
	runtime := loadRuntime()
	if runtime == nil {
		return nil
	}
	switch bucketType {
	case BucketTypeMem:
		if runtime.memory != nil {
			if bucket := runtime.memory.getBucket(name); bucket != nil {
				return bucket
			}
		}
	case BucketTypeDistMem:
		if runtime.distMem != nil {
			if bucket := runtime.distMem.getBucket(name); bucket != nil {
				return bucket
			}
		}
	case BucketTypeLevel2:
		if runtime.level2 != nil {
			if bucket := runtime.level2.getBucket(name); bucket != nil {
				return bucket
			}
		}
	case BucketTypeRedis:
		if runtime.redis != nil {
			if bucket := runtime.redis.getBucket(name); bucket != nil {
				return bucket
			}
		}
	}
	return nil
}
