package cachecloud

import (
	"bytes"
	"os"
	"runtime"
	"runtime/pprof"
	"strings"
	"testing"
	"time"
)

// TestResourceAuditInitFailure measures resources retained by failed construction.
// Run alone in a disposable test process; it intentionally exercises a leaking path.
func TestResourceAuditInitFailure(t *testing.T) {
	if os.Getenv("CLOUD_CACHE_RESOURCE_AUDIT") != "1" {
		t.Skip("opt-in resource audit")
	}
	if loadRuntime() != nil {
		t.Fatal("run this audit alone")
	}
	sample := func(label string) {
		runtime.GC()
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		var stacks bytes.Buffer
		_ = pprof.Lookup("goroutine").WriteTo(&stacks, 2)
		t.Logf("%s heap=%d objects=%d goroutines=%d cleaners=%d", label, m.HeapAlloc, m.HeapObjects, runtime.NumGoroutine(), strings.Count(stacks.String(), "bigcache/v3.newBigCache.func1"))
	}
	sample("before")
	for i := 0; i < 4; i++ {
		err := Init(Options{ServiceName: "init-failure-audit"}, NewMemBucketConfig("valid", time.Minute), NewMemBucketConfig("invalid", time.Millisecond))
		if err == nil || loadRuntime() != nil {
			t.Fatal("failed initialization published runtime")
		}
		sample("after failed init")
	}
}
