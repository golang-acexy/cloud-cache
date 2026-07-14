package test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-acexy/cloud-cache/cachecloud"
)

const (
	distMemChildEnv    = "CLOUD_CACHE_DIST_MEM_CHILD"
	distMemRoleEnv     = "CLOUD_CACHE_DIST_MEM_ROLE"
	distMemNodeEnv     = "CLOUD_CACHE_DIST_MEM_NODE"
	distMemActionEnv   = "CLOUD_CACHE_DIST_MEM_ACTION"
	distMemWorkDirEnv  = "CLOUD_CACHE_DIST_MEM_WORK_DIR"
	distMemServiceName = "cloud-cache-dist-mem-test"
	distMemBucketName  = cachecloud.BucketName("dist-memory-sync")
	distMemOtherBucket = cachecloud.BucketName("dist-memory-other")
)

func TestDistributedMemorySync(t *testing.T) {
	if os.Getenv(distMemChildEnv) == "1" {
		runDistMemChild(t)
		return
	}

	tests := []struct {
		name       string
		action     string
		wantResult string
	}{
		{name: "same value keeps local cache", action: "same", wantResult: "primary=hit:value-a;secondary=hit:value-a;other=hit:value-a"},
		{name: "different value evicts local cache", action: "different", wantResult: "primary=miss;secondary=hit:value-a;other=hit:value-a"},
		{name: "delete always evicts local cache", action: "delete", wantResult: "primary=miss;secondary=hit:value-a;other=hit:value-a"},
		{name: "dynamic key remains isolated", action: "different-key", wantResult: "primary=hit:value-a;secondary=miss;other=hit:value-a"},
		{name: "bucket remains isolated", action: "different-bucket", wantResult: "primary=hit:value-a;secondary=hit:value-a;other=miss"},
		{name: "service remains isolated", action: "different-service", wantResult: "primary=hit:value-a;secondary=hit:value-a;other=hit:value-a"},
		{name: "rapid updates evict stale value", action: "rapid-update", wantResult: "primary=miss;secondary=hit:value-a;other=hit:value-a"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runDistMemSyncScenario(t, test.action, test.wantResult)
		})
	}
}

func runDistMemSyncScenario(t *testing.T, action, wantResult string) {
	t.Helper()
	workDir := t.TempDir()
	binary, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test binary: %v", err)
	}

	holders := make([]*exec.Cmd, 0, 2)
	holderOutputs := make([]strings.Builder, 2)
	for i := range 2 {
		nodeName := fmt.Sprintf("holder-%d", i+1)
		holder := exec.Command(binary, "-test.run=^TestDistributedMemorySync$")
		holder.Env = append(os.Environ(),
			distMemChildEnv+"=1",
			distMemRoleEnv+"=holder",
			distMemNodeEnv+"="+nodeName,
			distMemWorkDirEnv+"="+workDir,
		)
		holder.Stdout = &holderOutputs[i]
		holder.Stderr = &holderOutputs[i]
		if err := holder.Start(); err != nil {
			t.Fatalf("start %s process: %v", nodeName, err)
		}
		holders = append(holders, holder)
	}
	defer func() {
		for _, holder := range holders {
			if holder.ProcessState == nil {
				_ = holder.Process.Kill()
				_ = holder.Wait()
			}
		}
	}()

	for i, output := range holderOutputs {
		readyFile := filepath.Join(workDir, fmt.Sprintf("ready-holder-%d", i+1))
		if !waitForFile(readyFile, 5*time.Second) {
			for _, holder := range holders {
				_ = holder.Process.Kill()
				_ = holder.Wait()
			}
			t.Fatalf("holder-%d did not become ready\n%s", i+1, output.String())
		}
	}
	updater := exec.Command(binary, "-test.run=^TestDistributedMemorySync$")
	updater.Env = append(os.Environ(),
		distMemChildEnv+"=1",
		distMemRoleEnv+"=updater",
		distMemNodeEnv+"=actor",
		distMemActionEnv+"="+action,
		distMemWorkDirEnv+"="+workDir,
	)
	if output, err := updater.CombinedOutput(); err != nil {
		_ = os.WriteFile(filepath.Join(workDir, "verify"), nil, 0o600)
		for _, holder := range holders {
			_ = holder.Wait()
		}
		t.Fatalf("updater process failed: %v\n%s", err, output)
	}

	// Redis Pub/Sub 是异步投递，给持有节点留出处理消息的时间。
	time.Sleep(300 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(workDir, "verify"), nil, 0o600); err != nil {
		t.Fatalf("signal holder verification: %v", err)
	}
	for i, holder := range holders {
		if err := holder.Wait(); err != nil {
			t.Fatalf("holder-%d process failed: %v\n%s", i+1, err, holderOutputs[i].String())
		}
		result, err := os.ReadFile(filepath.Join(workDir, fmt.Sprintf("result-holder-%d", i+1)))
		if err != nil {
			t.Fatalf("read holder-%d result: %v", i+1, err)
		}
		if string(result) != wantResult {
			t.Fatalf("unexpected holder-%d result: got %q, want %q\nholder output:\n%s", i+1, result, wantResult, holderOutputs[i].String())
		}
	}
}

func runDistMemChild(t *testing.T) {
	startRedis(t)
	action := os.Getenv(distMemActionEnv)
	serviceName := distMemServiceName
	if action == "different-service" {
		serviceName += "-isolated"
	}
	if err := cachecloud.Init(
		cachecloud.Options{ServiceName: serviceName},
		cachecloud.NewDistMemBucketConfig(distMemBucketName, time.Minute),
		cachecloud.NewDistMemBucketConfig(distMemOtherBucket, time.Minute),
	); err != nil {
		t.Fatalf("init distributed memory cache: %v", err)
	}
	key := cachecloud.NewCacheKey("sync-key:%s")
	workDir := os.Getenv(distMemWorkDirEnv)
	nodeName := os.Getenv(distMemNodeEnv)

	switch os.Getenv(distMemRoleEnv) {
	case "holder":
		for _, item := range []struct {
			bucket cachecloud.BucketName
			keyArg string
		}{
			{bucket: distMemBucketName, keyArg: "primary"},
			{bucket: distMemBucketName, keyArg: "secondary"},
			{bucket: distMemOtherBucket, keyArg: "primary"},
		} {
			if err := cachecloud.Put(item.bucket, key, "value-a", item.keyArg); err != nil {
				t.Fatalf("holder put initial value: %v", err)
			}
		}
		// SubscribeRetry 在后台建立订阅，确认后再通知父进程启动更新节点。
		time.Sleep(300 * time.Millisecond)
		if err := os.WriteFile(filepath.Join(workDir, "ready-"+nodeName), nil, 0o600); err != nil {
			t.Fatalf("write holder ready signal: %v", err)
		}
		if !waitForFile(filepath.Join(workDir, "verify"), 10*time.Second) {
			t.Fatal("wait for holder verification signal timeout")
		}
		result := strings.Join([]string{
			"primary=" + readDistMemValue(t, distMemBucketName, key, "primary"),
			"secondary=" + readDistMemValue(t, distMemBucketName, key, "secondary"),
			"other=" + readDistMemValue(t, distMemOtherBucket, key, "primary"),
		}, ";")
		if err := os.WriteFile(filepath.Join(workDir, "result-"+nodeName), []byte(result), 0o600); err != nil {
			t.Fatalf("write holder result: %v", err)
		}
	case "updater":
		// 确保更新节点的订阅已建立，避免自身初始化与发布操作发生竞争。
		time.Sleep(300 * time.Millisecond)
		switch action {
		case "same":
			if err := cachecloud.Put(distMemBucketName, key, "value-a", "primary"); err != nil {
				t.Fatalf("updater put same value: %v", err)
			}
		case "different":
			if err := cachecloud.Put(distMemBucketName, key, "value-b", "primary"); err != nil {
				t.Fatalf("updater put different value: %v", err)
			}
		case "delete":
			// 更新节点本地没有该 Key，仍必须广播删除事件。
			_ = cachecloud.Evict(distMemBucketName, key, "primary")
		case "different-key":
			if err := cachecloud.Put(distMemBucketName, key, "value-b", "secondary"); err != nil {
				t.Fatalf("updater put another key: %v", err)
			}
		case "different-bucket":
			if err := cachecloud.Put(distMemOtherBucket, key, "value-b", "primary"); err != nil {
				t.Fatalf("updater put another bucket: %v", err)
			}
		case "different-service":
			if err := cachecloud.Put(distMemBucketName, key, "value-b", "primary"); err != nil {
				t.Fatalf("updater put isolated service value: %v", err)
			}
		case "rapid-update":
			for _, value := range []string{"value-b", "value-c", "value-d"} {
				if err := cachecloud.Put(distMemBucketName, key, value, "primary"); err != nil {
					t.Fatalf("updater put rapid value: %v", err)
				}
			}
		default:
			t.Fatal(fmt.Errorf("unknown child action: %s", os.Getenv(distMemActionEnv)))
		}
	default:
		t.Fatalf("unknown child role: %s", os.Getenv(distMemRoleEnv))
	}
}

func readDistMemValue(t *testing.T, bucketName cachecloud.BucketName, key cachecloud.CacheKey, keyArg string) string {
	t.Helper()
	var value string
	err := cachecloud.Get(bucketName, key, &value, keyArg)
	switch {
	case err == nil:
		return "hit:" + value
	case errors.Is(err, cachecloud.ErrCacheMiss):
		return "miss"
	default:
		t.Fatalf("get distributed memory value: %v", err)
		return ""
	}
}

func waitForFile(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}
