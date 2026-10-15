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

| Bucket type | Constructor | Storage and miss behavior | Typical use |
| --- | --- | --- | --- |
| `mem` (local memory) | `NewMemBucketConfig` | Process-local memory; no Redis dependency or cross-node synchronization. | Local, disposable results. |
| `dist-mem` (distributed memory) | `NewDistMemBucketConfig` | Values stay in each node; Redis carries best-effort invalidation only. Each node loads its own misses. | Fast local reads of low-sensitivity data that can tolerate node differences. |
| `redis` | `NewRedisBucketConfig` | Reads and writes shared Redis directly; no local copy. | Shared cached data where local-copy divergence is undesirable. |
| `level-2` (L2) | `NewLevel2BucketConfig` | Reads local L1 first, then shared Redis L2; a Redis hit rebuilds L1. Redis also carries best-effort L1 invalidation. | Read-heavy, low-sensitivity data benefiting from local speed and shared cached results. |

`Get` returns `ErrCacheMiss` when the selected storage path has no value; `Cacheable` can then invoke your Loader. Automatic Redis-to-L1 rebuilding does not publish synchronization messages.

Distributed-memory and level-2 buckets favor fast local reads and low overhead. Use them for data whose correctness requirements tolerate stale reads and temporary differences between nodes, such as display information or recomputable results. Synchronization can be delayed, lost, or reordered during startup, disconnects, overload, or concurrent operations. These modes do not guarantee strong consistency, reliable event delivery, or convergence within a fixed time.

Do not use their cached values as the sole authority for permission revocation, balances, inventory deductions, or other decisions requiring immediate accuracy; validate those decisions against the authoritative data source. Plain memory has no cross-node synchronization. Redis-only buckets avoid local-copy divergence but do not make cache updates transactional with the application database.

Choose TTLs according to acceptable staleness and reload cost. Expiration helps recovery, but same-value renewal and stale loader writes mean that one TTL is not a guaranteed deadline for seeing the latest source data. Best-effort synchronization still aims for accuracy and stability through inexpensive validation, expiration checks, local concurrency protection, and error handling.

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
	CatalogBucket    cachecloud.BucketName = "catalog"
	ProfileBucket    cachecloud.BucketName = "profile"
	DisplayBucket    cachecloud.BucketName = "display"
)

err := cachecloud.Init(
	cachecloud.Options{ServiceName: "user-service"},
	cachecloud.NewMemBucketConfig(UserMemoryBucket, 5*time.Minute),
	cachecloud.NewDistMemBucketConfig(CatalogBucket, 10*time.Minute),
	cachecloud.NewRedisBucketConfig(ProfileBucket, time.Hour),
	cachecloud.NewLevel2BucketConfig(
		DisplayBucket,
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
- Every bucket has a non-empty name. Service and bucket names must not contain `:` or `%`.
- Expiration durations are greater than zero. Memory TTLs (including distributed memory and L1) must also be at least one second for the current BigCache backend.
- A bucket name is registered only once.
- Every bucket uses a supported cache type.

The cache runtime is process-wide and may only be initialized once. A second valid call to `Init` returns `ErrAlreadyInitialized`; configuration validation runs first. Initialization does not verify Redis connectivity or wait for subscriptions. There is no public shutdown, reset, or reconfiguration API, and failed construction does not roll back all allocated resources.

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
		DisplayBucket,
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

The same key arguments must be supplied to `Get`, `Put`, and `Evict`. Without arguments, the format is used verbatim. Formatting errors are not rejected. Initialization rejects service/bucket names containing `:` or `%` with `ErrInvalidCacheName`. Distributed-memory and level-2 operations reject empty resolved keys with `ErrCacheKeyRequired` before accessing storage or publishing. Their resolved keys may contain literal `:` and `%`.

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

`Get` requires a non-nil result pointer. Package-level calls reject nil and typed-nil pointers with `ErrResultRequired`; direct bucket calls bypass this check. Other invalid destinations fail through the codec. Use a fresh, compatible result for each read; Gob decoding may retain fields in reused destinations or partially change them before an error.

All storage modes support cached nil results. Nil interfaces, nil pointers (including pointer/interface chains), and nil maps/slices use an empty payload. A hit on this payload clears the destination to its zero value and returns nil; it is distinct from `ErrCacheMiss`. Non-nil empty strings, zero scalars, and empty collections still use Gob. Redis storage uses `GetBytes`/`SetBytes`, and local storage uses the toolkit's byte APIs; this module owns value encoding. Distributed modes retain their envelope, TTL, and invalidation metadata for nil values.

For a nullable object, use `var user *User` and pass `&user` to `Get` or `Cacheable`. A Loader returning `true, nil` with `*result == nil` caches the missing result; `false, nil` still skips storage. `CacheableSingleflight` also supports nil results, including uncached results shared with current waiters. Nil result destinations themselves remain invalid.

Non-nil Gob payloads remain compatible with existing entries. Older module versions cannot decode the new empty payload representation; upgrade all participating readers before writing nil results.

### Evict

```go
if err := cachecloud.Evict(ProfileBucket, key, userID); err != nil {
	return err
}
```

Deletion is idempotent in all four modes: an absent entry succeeds, while storage or publication failures are still returned.

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
		loaded, err := userRepository.QueryByID(userID)
		if err != nil {
			return false, err
		}
		if loaded == nil {
			return false, ErrUserNotFound
		}
		*result = *loaded
		return true, nil
	},
	userID,
)
if err != nil {
	return err
}
```

This example uses the RDS Repository missing-result convention (`nil, nil`). Mongo repositories return `mongo.ErrNoDocuments`, which the loader must handle explicitly.

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
- Cache infrastructure or decoding errors do not trigger the Loader. A cache write failure is returned even if loading already populated the result.
- Concurrent misses are not coalesced. A Loader running during a write or eviction can later store stale data; there is no automatic source transaction or singleflight.

## Coalescing Concurrent Misses

Use `CacheableSingleflight` when concurrent requests for one key should share a load:

```go
var user User
err := cachecloud.CacheableSingleflight(
    ProfileBucket, cachecloud.NewCacheKey("user:%d"), &user,
    func(result *User) (bool, error) {
        loaded, err := userRepository.QueryByID(userID)
        if err != nil {
            return false, err
        }
        if loaded == nil {
            return false, ErrUserNotFound
        }
        *result = *loaded
        return true, nil
    },
    userID,
)
```

This works with all four bucket types. Cache hits bypass coordination. Overlapping misses share one operation within this process, grouped by bucket, resolved key, and result type. The selected operation checks the cache again before invoking its Loader and writes at most once. The existing `Cacheable` continues to load concurrent misses independently.

- Each call needs its own result pointer. The Loader receives a fresh zero value, and callers receive independent Gob-decoded snapshots, including maps and slices. A successful read replaces the destination instead of merging into its old contents.
- Loaded results must be nil or support Gob even with `false, nil`: current waiters share that uncached result, while later misses load again. Custom codecs must decode deterministically and preserve independent ownership; this API performs additional encoding/decoding.
- Loaders sharing a key must have equivalent behavior. Include all relevant tenant, user, and query inputs in the key. Different result types do not share flights, but they still address the same stored key and must remain storage-compatible.
- Loader failures are shared without copying partially loaded values. Storage errors remain errors; a write error still returns the loaded result when snapshot decoding succeeds. Snapshot encoding/validation errors occur before the write. A nil Loader returns a miss without joining pending work.
- A Loader must not recursively call this API for the same bucket, key, and type. Waiting has no cancellation or timeout. Loader panics propagate through singleflight; `runtime.Goexit` terminates participating goroutines.
- Coordination is process-local. It does not prevent stale Loader writes after `Put`/`Evict`, provide distributed locking, or make shared Loader state safe for concurrent use across different keys.

## Distributed Memory Synchronization

A distributed-memory bucket stores values only in each process's memory. Redis Pub/Sub carries invalidation events; Redis does not store the cached value.

Remote writes do not transfer values: nodes without the key remain empty and load independently. Different hashes invalidate local values; equal hashes may extend their deadlines within the local TTL cap when the extension exceeds three seconds. Equal maps produce stable hashes regardless of insertion order. Comparison uses the Gob-decoded value and includes stored fields omitted by JSON. Use a consistent module version and type definitions across participating nodes.

When one node calls `Evict`, it attempts to publish a delete event regardless of the local deletion result. Other nodes then evict the same bucket and cache key.

Synchronization is isolated by both `ServiceName` and `BucketName`. Messages from the publishing node itself are ignored, and applying a remote event never publishes another event.

Important behavior:

- `Cacheable` rebuilds remain node-local values; Redis transports synchronization metadata, not the cached payload.
- Equal-value events may extend an entry only up to the receiving bucket's configured expiration limit.
- Expiration times use Unix milliseconds and are independent of time zones, but participating hosts should keep their system clocks synchronized.
- Redis Pub/Sub does not replay messages missed while a node is disconnected. Expiration and reloading help recovery, but renewal and stale loader writes prevent a guaranteed convergence deadline.

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
write Redis -> write local L1 -> publish invalidation metadata
```

Other nodes keep their L1 value when the hash is unchanged. A different hash evicts their L1 value, so the next read rebuilds it from Redis.

Delete flow attempts all three operations:

```text
delete Redis -> delete local L1 -> publish delete event
```

Errors from these operations are combined where appropriate so a local failure does not prevent cross-node invalidation from being attempted.

## Stored Values and Equality

All buckets accept nil results or Gob-compatible values with matching read types. Keep inputs unchanged during writes. Interface fields may require `gob.Register` for their concrete types; custom Gob codecs must round-trip into a fresh value and should decode deterministically without external side effects.

Distributed-memory and L2 compare decoded values using a stable digest. Supported values include scalars, exported-field structs, nested maps, slices, arrays, pointers, `time.Time`, `time.Duration`, `decimal.Decimal`, `big.Int`, `big.Rat`, `big.Float`, `json.Number`, `net.IP`, `netip.Addr`, UUIDs, and SQL nullable values, provided they also support Gob storage. Map order and pointer addresses do not affect equality; sequence order and stored field changes do.

Equality follows normalized value semantics: times compare by instant, decimals by numeric value, floating signed zeros compare equal, and NaN representations are normalized. Gob itself may discard distinctions such as nil versus empty slices. Same-digest notifications retain the existing local representation; store representation-sensitive information, such as a time-zone name or decimal scale, in explicit fields if it must trigger invalidation. `json.RawMessage` remains raw bytes, not normalized JSON.

Gob-compatible values outside the digest's supported types or traversal limits fall back to a Gob-byte digest. They remain storable but may cause extra invalidations, including maps with pointer keys or custom types containing private fields. Gob encoding or round-trip decoding failures abort distributed-memory/L2 writes before storage. Digests are change-detection aids, not security checks or proofs of equality.

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
| `ErrInvalidCacheName` | A service or bucket name contains `:` or `%`. |
| `ErrCacheKeyRequired` | A distributed-memory or level-2 operation resolved an empty key. |

## Design Notes

- `cloud-cache` is a cloud component, not a Starter. It does not own the Redis lifecycle.
- Redis-backed modes require `starter-redis` to be started first.
- Cache configuration and bucket routing become immutable after successful initialization.
- Bucket implementations are created eagerly during initialization, so runtime reads do not mutate routing maps.
- Duplicate bucket names are rejected instead of being resolved through an implicit priority order.
- Distributed synchronization uses structured JSON events and exact node ID comparison.
- Distributed-memory and L2 writes compute a stable digest from the Gob-decoded value before storage; expiration is excluded. Reads and automatic L1 rebuilding reuse the stored digest.
- Cache values use the Gob codecs provided by the underlying toolkit and Redis starter implementations.

## Expiration and Failure Boundaries

- Plain memory uses BigCache cleanup (currently a five-second cleanup interval) and does not check TTL on each read. Expired values can remain readable until cleanup; do not use this mode as a strict validity deadline. Distributed memory and level-2 entries add millisecond logical deadline checks.
- Reads do not renew TTL. Tiny positive L2 Redis TTLs can round to an immediately expired logical deadline; memory TTLs below one second are rejected by the backend. L1 is capped by the L2 envelope deadline, but configuration does not require its TTL to be shorter.
- Pub/Sub publication success does not acknowledge remote processing. Startup, disconnects, overload, and event reordering can cause stale reads; synchronization is not strong consistency.
- Level-2 Put can update Redis before L1 or publication fails. A failed L1 write triggers best-effort local eviction before publication; cleanup errors are also returned. Evict attempts all three steps and joins errors; a failed Redis deletion can let old L2 data repopulate L1. Errors do not imply unchanged state.
- Level-2 refill rechecks expiration before filling L1 and after exhausting three interference retries. An expired snapshot returns `ErrCacheMiss`; a still-valid snapshot may be returned without filling L1. This does not guarantee the latest value. Source Loader execution is outside this protection.
- Redis operations and subscriptions do not inherit request cancellation through this API. Finish application work before shutting down starter-redis; cache-owned subscription retries have no public cancellation handle.

## Testing

Run `go test -race ./cachecloud` for package tests and `go test -race ./test -run '^TestMemoryCacheAndCacheable$'` for the public memory flow. With a configured test Redis cluster, run `go test -race -count=1 -timeout=240s ./...` for all four modes, expiration, concurrent access, and cross-process invalidation. `TestAllCacheModes` also verifies that rebuilding L1 from Redis does not publish an event. Redis tests use external infrastructure and some launch multiple processes.
