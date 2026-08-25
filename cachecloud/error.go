package cachecloud

import "errors"

var (
	ErrCacheMiss             = errors.New("cache miss")
	ErrBucketNotFound        = errors.New("cache bucket not found")
	ErrServiceNameRequired   = errors.New("cache service name is required")
	ErrBucketConfigRequired  = errors.New("at least one cache bucket config is required")
	ErrBucketNameRequired    = errors.New("cache bucket name is required")
	ErrInvalidExpiration     = errors.New("cache expiration must be greater than zero")
	ErrDuplicateBucket       = errors.New("duplicate cache bucket")
	ErrUnsupportedBucketType = errors.New("unsupported cache bucket type")
	ErrAlreadyInitialized    = errors.New("cache cloud already initialized")
	ErrNotInitialized        = errors.New("cache cloud not initialized")
	ErrResultRequired        = errors.New("cache result is required")
	ErrInvalidSyncEvent      = errors.New("invalid cache sync event")
)
