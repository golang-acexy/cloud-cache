package cachecloud

import "errors"

var (
	// ErrInvalidCacheName indicates a service or bucket name containing ':' or '%'.
	ErrInvalidCacheName = errors.New("cache service and bucket names must not contain ':' or '%'")
	// ErrCacheKeyRequired indicates an empty resolved key in a synchronized bucket.
	ErrCacheKeyRequired = errors.New("cache key is required")
	// ErrCacheMiss indicates an absent or logically expired entry.
	ErrCacheMiss = errors.New("cache miss")
	// ErrBucketNotFound indicates an unconfigured bucket name.
	ErrBucketNotFound = errors.New("cache bucket not found")
	// ErrServiceNameRequired indicates a blank service namespace.
	ErrServiceNameRequired = errors.New("cache service name is required")
	// ErrBucketConfigRequired indicates an empty bucket configuration list.
	ErrBucketConfigRequired = errors.New("at least one cache bucket config is required")
	// ErrBucketNameRequired indicates a blank bucket name.
	ErrBucketNameRequired = errors.New("cache bucket name is required")
	// ErrInvalidExpiration indicates a nonpositive required TTL.
	ErrInvalidExpiration = errors.New("cache expiration must be greater than zero")
	// ErrDuplicateBucket indicates a repeated bucket name across any modes.
	ErrDuplicateBucket = errors.New("duplicate cache bucket")
	// ErrUnsupportedBucketType indicates an unsupported storage mode.
	ErrUnsupportedBucketType = errors.New("unsupported cache bucket type")
	// ErrAlreadyInitialized indicates an already published runtime.
	ErrAlreadyInitialized = errors.New("cache cloud already initialized")
	// ErrNotInitialized indicates cache use before successful Init.
	ErrNotInitialized = errors.New("cache cloud not initialized")
	// ErrResultRequired indicates a nil result destination rejected by package-level APIs.
	ErrResultRequired = errors.New("cache result is required")
	// ErrInvalidSyncEvent identifies malformed synchronization messages, which are logged and discarded.
	ErrInvalidSyncEvent = errors.New("invalid cache sync event")
)
