package cachecloud

import (
	"errors"
	"testing"
	"time"
)

func resetRuntimeForTest(t *testing.T) {
	t.Helper()
	runtimeState.Lock()
	runtimeState.instance = nil
	runtimeState.Unlock()
	t.Cleanup(func() {
		runtimeState.Lock()
		runtimeState.instance = nil
		runtimeState.Unlock()
	})
}

func TestValidateConfig(t *testing.T) {
	valid := NewMemBucketConfig("valid", time.Minute)
	tests := []struct {
		name    string
		options Options
		configs []BucketConfig
		wantErr error
	}{
		{name: "service required", configs: []BucketConfig{valid}, wantErr: ErrServiceNameRequired},
		{name: "bucket required", options: Options{ServiceName: "test"}, wantErr: ErrBucketConfigRequired},
		{name: "bucket name required", options: Options{ServiceName: "test"}, configs: []BucketConfig{NewMemBucketConfig("", time.Minute)}, wantErr: ErrBucketNameRequired},
		{name: "expiration required", options: Options{ServiceName: "test"}, configs: []BucketConfig{NewMemBucketConfig("invalid", 0)}, wantErr: ErrInvalidExpiration},
		{name: "duplicate bucket", options: Options{ServiceName: "test"}, configs: []BucketConfig{valid, NewRedisBucketConfig("valid", time.Minute)}, wantErr: ErrDuplicateBucket},
		{name: "unsupported type", options: Options{ServiceName: "test"}, configs: []BucketConfig{{bucketName: "invalid", bucketType: "unknown"}}, wantErr: ErrUnsupportedBucketType},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateConfig(test.options, test.configs); !errors.Is(err, test.wantErr) {
				t.Fatalf("unexpected validation error: got %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestInitRejectsDuplicateInitialization(t *testing.T) {
	resetRuntimeForTest(t)
	if err := Init(Options{ServiceName: "test"}, NewMemBucketConfig("memory", time.Minute)); err != nil {
		t.Fatalf("first init: %v", err)
	}
	if err := Init(Options{ServiceName: "test"}, NewMemBucketConfig("other", time.Minute)); !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("expected duplicate initialization error, got %v", err)
	}
}

func TestPublicOperationsRequireInitialization(t *testing.T) {
	resetRuntimeForTest(t)
	key := NewCacheKey("key")
	var result string
	if err := Get("missing", key, &result); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("expected not initialized error, got %v", err)
	}
	if err := Put("missing", key, "value"); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("expected not initialized error, got %v", err)
	}
	if err := Evict("missing", key); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("expected not initialized error, got %v", err)
	}
}

func TestCacheableErrorBranches(t *testing.T) {
	resetRuntimeForTest(t)
	bucketName := BucketName("cacheable")
	if err := Init(Options{ServiceName: "test"}, NewMemBucketConfig(bucketName, time.Minute)); err != nil {
		t.Fatalf("init: %v", err)
	}
	key := NewCacheKey("key:%d")

	if err := Cacheable[string](bucketName, key, nil, nil, 1); !errors.Is(err, ErrResultRequired) {
		t.Fatalf("expected result required error, got %v", err)
	}

	var result string
	supplierErr := errors.New("supplier failed")
	if err := Cacheable(bucketName, key, &result, func() (*string, error) {
		return nil, supplierErr
	}, 1); !errors.Is(err, supplierErr) {
		t.Fatalf("expected supplier error, got %v", err)
	}
	if err := Cacheable(bucketName, key, &result, func() (*string, error) {
		return nil, nil
	}, 1); !errors.Is(err, ErrSupplierReturnedNil) {
		t.Fatalf("expected nil supplier value error, got %v", err)
	}
}
