package cachecloud

var level2Cache *SecondLevelCacheManager

// SecondLevelCacheManager 二级缓存管理器
type SecondLevelCacheManager struct {
}

func (*SecondLevelCacheManager) getBucket(bucketName BucketName) CacheBucket {
	return nil
}
