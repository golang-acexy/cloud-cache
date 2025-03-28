package cachecloud

var level2Cache *secondLevelCacheManager

// secondLevelCacheManager 二级缓存管理器
type secondLevelCacheManager struct {
}

func (*secondLevelCacheManager) getBucket(bucketName BucketName) CacheBucket {
	return nil
}
