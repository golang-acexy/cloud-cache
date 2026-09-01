# cloud-cache

`cloud-cache` provides one cache API for the golang-acexy cloud ecosystem. It combines the in-memory cache implementation from `golang-toolkit/caching` with `starter-redis` and supports local memory, synchronized distributed memory, Redis, and two-level cache buckets.

## Ecosystem Role

This package owns cache policy rather than Redis lifecycle. It standardizes application cache access and cache-aside loading while delegating Redis connectivity, Pub/Sub, and shutdown to `starter-redis`.

## Requirements

Current module Go version: `1.26.7`.

## Installation

```bash
go get github.com/golang-acexy/cloud-cache
```

Redis-backed modes also require:

```bash
go get github.com/golang-acexy/starter-redis
```

## Cache Modes

| Mode | Constructor | Storage and behavior |
| --- | --- | --- |
| Local memory | `NewMemBucketConfig` | Process-local BigCache storage. |
| Distributed memory | `NewDistMemBucketConfig` | Process-local memory with Redis Pub/Sub invalidation between nodes. |
| Redis | `NewRedisBucketConfig` | Centralized Redis storage. |
| Level 2 | `NewLevel2BucketConfig` | Local L1 memory backed by shared L2 Redis storage and cross-node L1 invalidation. |

All modes implement the same `CacheBucket` operations:

```go
type CacheBucket interface {
	Get(key CacheKey, result any, keyArgs ...any) error
	Put(key CacheKey, data any, keyArgs ...any) error
	Evict(key CacheKey, keyArgs ...any) error
}
```

## Initialization

Initialize all cache buckets once before application code accesses the cache:

```go
const (
	UserMemoryBucket cachecloud.BucketName = "user-memory"
	SessionBucket    cachecloud.BucketName = "session"
	ProfileBucket    cachecloud.BucketName = "profile"
	PermissionBucket cachecloud.BucketName = "permission"
)

err := cachecloud.Init(
	cachecloud.Options{ServiceName: "user-service"},
	cachecloud.NewMemBucketConfig(UserMemoryBucket, 5*time.Minute),
	cachecloud.NewDistMemBucketConfig(SessionBucket, 10*time.Minute),
	cachecloud.NewRedisBucketConfig(ProfileBucket, time.Hour),
	cachecloud.NewLevel2BucketConfig(
		PermissionBucket,
		5*time.Minute,
		24*time.Hour,
	),
)
if err != nil {
	panic(err)
}
```

`ServiceName` is required and isolates Redis keys and synchronization topics between applications.

Initialization validates that:

- At least one bucket is configured.
- Every bucket has a non-empty name.
- Expiration durations are greater than zero.
- A bucket name is registered only once.
- Every bucket uses a supported cache type.

The cache runtime is process-wide and may only be initialized once. A second call to `Init` returns `ErrAlreadyInitialized`.

## Redis Startup Order

Start `starter-redis` before calling `cachecloud.Init` when any distributed memory, Redis, or level-2 bucket is configured:

```go
redisStarter := &redisstarter.RedisStarter{
	Config: redisstarter.RedisConfig{
		UniversalOptions: redis.UniversalOptions{
			Addrs:    []string{"127.0.0.1:6379"},
			Password: "YOUR_REDIS_PASSWORD",
		},
	},
}

loader := parent.InitStarterLoader([]parent.Starter{redisStarter})
if err := loader.Start(); err != nil {
	panic(err)
}

if err := cachecloud.Init(
	cachecloud.Options{ServiceName: "user-service"},
	cachecloud.NewLevel2BucketConfig(
		PermissionBucket,
		5*time.Minute,
		24*time.Hour,
	),
); err != nil {
	panic(err)
}
```

Pure local-memory buckets do not execute Redis commands, but `ServiceName` remains required so configuration stays consistent across modes.

## Cache Keys

Create a static cache key:

```go
key := cachecloud.NewCacheKey("current-user")
```

Dynamic key arguments are formatted with `fmt.Sprintf`:

```go
userKey := cachecloud.NewCacheKey("user:%d")

err := cachecloud.Put(ProfileBucket, userKey, user, user.ID)

var result User
err = cachecloud.Get(ProfileBucket, userKey, &result, user.ID)
```

The same key arguments must be supplied to `Get`, `Put`, and `Evict`.

## Common Operations

### Put and Get

```go
type User struct {
	ID   uint64
	Name string
}

key := cachecloud.NewCacheKey("user:%d")
want := User{ID: 1001, Name: "Alice"}

if err := cachecloud.Put(ProfileBucket, key, want, want.ID); err != nil {
	return err
}

var got User
if err := cachecloud.Get(ProfileBucket, key, &got, want.ID); err != nil {
	return err
}
```

`Get` requires a non-nil result pointer. Passing a nil result returns `ErrResultRequired`.

### Evict

```go
if err := cachecloud.Evict(ProfileBucket, key, userID); err != nil {
	return err
}
```

Redis deletion is idempotent: deleting a key that does not exist still succeeds unless Redis itself returns an error.

### Resolve a Bucket

Use `GetBucket` when an integration needs the common bucket interface:

```go
bucket := cachecloud.GetBucket(ProfileBucket)
if bucket == nil {
	return cachecloud.ErrBucketNotFound
}
```

Use `GetBucketByType` when the expected cache mode is known:

```go
bucket := cachecloud.GetBucketByType(
	ProfileBucket,
	cachecloud.BucketTypeRedis,
)
```

Applications should normally prefer the package-level `Get`, `Put`, and `Evict` functions because they return explicit initialization and bucket errors.

## Cacheable

`Cacheable` reads the cache first and invokes a Loader only after `ErrCacheMiss`:

```go
var user User
err := cachecloud.Cacheable(
	ProfileBucket,
	cachecloud.NewCacheKey("user:%d"),
	&user,
	func(result *User) (bool, error) {
		rows, err := userRepository.QueryByID(userID, result)
		if err != nil {
			return false, err
		}
		if rows == 0 {
			return false, ErrUserNotFound
		}
		return true, nil
	},
	userID,
)
if err != nil {
	return err
}
```

The Loader type is:

```go
type Loader[T any] func(result *T) (cacheable bool, err error)
```

Behavior:

- A cache hit fills the result without invoking the Loader.
- A cache miss invokes the Loader with the same result pointer.
- A Loader result of `true, nil` stores the loaded result; `false, nil` returns it without caching.
- A Loader error is returned unchanged and the result is not cached.
- A nil Loader returns `ErrCacheMiss` when the cache does not contain the key.
- A nil result pointer returns `ErrResultRequired`.

## Distributed Memory Synchronization

A distributed-memory bucket stores values only in each process's memory. Redis Pub/Sub carries invalidation events; Redis does not store the cached value.

Each local entry is stored in an internal envelope containing:

```text
Gob payload + content hash + absolute expiration time
```

Gob remains the storage encoding. The content hash uses XXH3-128 over deterministic JSON when the value supports JSON encoding, which keeps logically equal maps stable across nodes even when their insertion order differs. Values that JSON cannot encode fall back to hashing the stored Gob payload.

When one node creates or updates a value:

1. The value is encoded once into the envelope and written to local memory.
2. The node publishes a `put` event containing the node ID, bucket name, resolved key, content hash, and absolute expiration time.
3. A receiving node ignores the event when it does not hold that key.
4. A receiving node evicts its local value when the hashes differ.
5. Equal values remain cached. If the remote expiration is more than three seconds later, the receiver silently extends its local expiration without publishing another event.

This comparison prevents nodes that concurrently load the same logical value from repeatedly evicting each other. Local reads, writes, deletes, and remote synchronization for the same bucket and key are serialized through fixed synchronization shards so an expiration refresh cannot overwrite a concurrent local update.

When one node calls `Evict`, it attempts to publish a delete event regardless of the local deletion result. Other nodes then evict the same bucket and cache key.

Synchronization is isolated by both `ServiceName` and `BucketName`. Messages from the publishing node itself are ignored, and applying a remote event never publishes another event.

Important behavior:

- `Cacheable` rebuilds remain node-local values; Redis transports synchronization metadata, not the cached payload.
- Equal-value events may extend an entry only up to the receiving bucket's configured expiration limit.
- Expiration times use Unix milliseconds and are independent of time zones, but participating hosts should keep their system clocks synchronized.
- Redis Pub/Sub does not replay messages missed while a node is disconnected. Local expiration remains the final convergence boundary after a missed event.

## Level-2 Cache

A level-2 bucket combines:

- L1: process-local memory for fast reads.
- L2: shared Redis storage as the authoritative cached value.

Read flow:

```text
L1 hit  -> return local value
L1 miss -> read Redis -> rebuild L1 -> return value
```

Write flow:

```text
write Redis -> write local L1 -> publish stored-byte hash
```

Other nodes keep their L1 value when the hash is unchanged. A different hash evicts their L1 value, so the next read rebuilds it from Redis.

Delete flow attempts all three operations:

```text
delete Redis -> delete local L1 -> publish delete event
```

Errors from these operations are combined where appropriate so a local failure does not prevent cross-node invalidation from being attempted.

## Common Errors

Use `errors.Is` when handling exported errors:

```go
if errors.Is(err, cachecloud.ErrCacheMiss) {
	// Load the value from its source or return a not-found response.
}
```

| Error | Meaning |
| --- | --- |
| `ErrCacheMiss` | The requested cache key is not present. |
| `ErrBucketNotFound` | The requested bucket was not configured. |
| `ErrServiceNameRequired` | `Options.ServiceName` is empty. |
| `ErrBucketConfigRequired` | No bucket configuration was supplied. |
| `ErrBucketNameRequired` | A bucket has an empty name. |
| `ErrInvalidExpiration` | A required expiration duration is not greater than zero. |
| `ErrDuplicateBucket` | A bucket name was configured more than once. |
| `ErrUnsupportedBucketType` | A bucket has an unsupported cache type. |
| `ErrAlreadyInitialized` | The process-wide cache runtime was already initialized. |
| `ErrNotInitialized` | A cache operation was used before `Init`. |
| `ErrResultRequired` | A nil result pointer was passed to `Get` or `Cacheable`. |
| `ErrInvalidSyncEvent` | A distributed invalidation message is malformed. |

## Design Notes

- `cloud-cache` is a cloud component, not a Starter. It does not own the Redis lifecycle.
- Redis-backed modes require `starter-redis` to be started first.
- Cache configuration and bucket routing become immutable after successful initialization.
- Bucket implementations are created eagerly during initialization, so runtime reads do not mutate routing maps.
- Duplicate bucket names are rejected instead of being resolved through an implicit priority order.
- Distributed synchronization uses structured JSON events and exact node ID comparison.
- Synchronization hashes are calculated from bytes read back from the actual memory cache, not from the value before storage.
- Cache values use the Gob codecs provided by the underlying toolkit and Redis starter implementations.
