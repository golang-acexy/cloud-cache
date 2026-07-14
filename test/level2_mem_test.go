package test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-acexy/cloud-cache/cachecloud"
	"github.com/golang-acexy/starter-redis/redisstarter"
)

const (
	level2ChildEnv    = "CLOUD_CACHE_LEVEL2_CHILD"
	level2RoleEnv     = "CLOUD_CACHE_LEVEL2_ROLE"
	level2NodeEnv     = "CLOUD_CACHE_LEVEL2_NODE"
	level2ActionEnv   = "CLOUD_CACHE_LEVEL2_ACTION"
	level2WorkDirEnv  = "CLOUD_CACHE_LEVEL2_WORK_DIR"
	level2ServiceName = "cloud-cache-level2-test"
	level2BucketName  = cachecloud.BucketName("level2-sync")
	level2OtherBucket = cachecloud.BucketName("level2-other")
)

func TestLevel2CacheSync(t *testing.T) {
	if os.Getenv(level2ChildEnv) == "1" {
		runLevel2Child(t)
		return
	}

	tests := []struct {
		name       string
		action     string
		wantResult string
	}{
		{name: "same value keeps level1 cache", action: "same", wantResult: "primary=hit:value-a;secondary=hit:value-a;other=hit:value-a"},
		{name: "different value rebuilds from Redis", action: "different-rebuild", wantResult: "primary=hit:value-b;secondary=hit:value-a;other=hit:value-a"},
		{name: "different value evicts level1 cache", action: "different", wantResult: "primary=miss;secondary=hit:value-a;other=hit:value-a"},
		{name: "delete always evicts level1 cache", action: "delete", wantResult: "primary=miss;secondary=hit:value-a;other=hit:value-a"},
		{name: "dynamic key remains isolated", action: "different-key", wantResult: "primary=hit:value-a;secondary=miss;other=hit:value-a"},
		{name: "bucket remains isolated", action: "different-bucket", wantResult: "primary=hit:value-a;secondary=hit:value-a;other=miss"},
		{name: "service remains isolated", action: "different-service", wantResult: "primary=hit:value-a;secondary=hit:value-a;other=hit:value-a"},
		{name: "rapid updates evict stale level1 value", action: "rapid-update", wantResult: "primary=miss;secondary=hit:value-a;other=hit:value-a"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runLevel2SyncScenario(t, test.action, test.wantResult)
		})
	}
}

func runLevel2SyncScenario(t *testing.T, action, wantResult string) {
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
		holder := exec.Command(binary, "-test.run=^TestLevel2CacheSync$")
		holder.Env = append(os.Environ(),
			level2ChildEnv+"=1",
			level2RoleEnv+"=holder",
			level2NodeEnv+"="+nodeName,
			level2WorkDirEnv+"="+workDir,
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
		if !waitForFile(filepath.Join(workDir, fmt.Sprintf("ready-holder-%d", i+1)), 5*time.Second) {
			for _, holder := range holders {
				_ = holder.Process.Kill()
				_ = holder.Wait()
			}
			t.Fatalf("holder-%d did not become ready\n%s", i+1, output.String())
		}
	}

	actor := exec.Command(binary, "-test.run=^TestLevel2CacheSync$")
	actor.Env = append(os.Environ(),
		level2ChildEnv+"=1",
		level2RoleEnv+"=actor",
		level2NodeEnv+"=actor",
		level2ActionEnv+"="+action,
		level2WorkDirEnv+"="+workDir,
	)
	if output, err := actor.CombinedOutput(); err != nil {
		_ = os.WriteFile(filepath.Join(workDir, "verify"), nil, 0o600)
		for _, holder := range holders {
			_ = holder.Wait()
		}
		t.Fatalf("actor process failed: %v\n%s", err, output)
	}

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

func runLevel2Child(t *testing.T) {
	startRedis(t)
	action := os.Getenv(level2ActionEnv)
	serviceName := level2ServiceName
	if action == "different-service" {
		serviceName += "-isolated"
	}
	if err := cachecloud.Init(
		cachecloud.Options{ServiceName: serviceName},
		cachecloud.NewLevel2BucketConfig(level2BucketName, time.Minute, time.Minute),
		cachecloud.NewLevel2BucketConfig(level2OtherBucket, time.Minute, time.Minute),
	); err != nil {
		t.Fatalf("init level2 cache: %v", err)
	}
	key := cachecloud.NewCacheKey("sync-key:%s")
	workDir := os.Getenv(level2WorkDirEnv)
	nodeName := os.Getenv(level2NodeEnv)

	switch os.Getenv(level2RoleEnv) {
	case "holder":
		for _, item := range []struct {
			bucket cachecloud.BucketName
			keyArg string
		}{
			{bucket: level2BucketName, keyArg: "primary"},
			{bucket: level2BucketName, keyArg: "secondary"},
			{bucket: level2OtherBucket, keyArg: "primary"},
		} {
			if err := cachecloud.Put(item.bucket, key, "value-a", item.keyArg); err != nil {
				t.Fatalf("holder put initial value: %v", err)
			}
		}
		time.Sleep(300 * time.Millisecond)
		if err := os.WriteFile(filepath.Join(workDir, "ready-"+nodeName), nil, 0o600); err != nil {
			t.Fatalf("write holder ready signal: %v", err)
		}
		if !waitForFile(filepath.Join(workDir, "verify"), 10*time.Second) {
			t.Fatal("wait for holder verification signal timeout")
		}
		result := strings.Join([]string{
			"primary=" + readLevel2Value(t, level2BucketName, key, "primary"),
			"secondary=" + readLevel2Value(t, level2BucketName, key, "secondary"),
			"other=" + readLevel2Value(t, level2OtherBucket, key, "primary"),
		}, ";")
		if err := os.WriteFile(filepath.Join(workDir, "result-"+nodeName), []byte(result), 0o600); err != nil {
			t.Fatalf("write holder result: %v", err)
		}
	case "actor":
		time.Sleep(300 * time.Millisecond)
		rawDelete := false
		deleteBucket := level2BucketName
		deleteKeyArg := "primary"
		switch action {
		case "same":
			mustPutLevel2(t, level2BucketName, key, "value-a", "primary")
			rawDelete = true
		case "different-rebuild":
			mustPutLevel2(t, level2BucketName, key, "value-b", "primary")
		case "different":
			mustPutLevel2(t, level2BucketName, key, "value-b", "primary")
			rawDelete = true
		case "delete":
			_ = cachecloud.Evict(level2BucketName, key, "primary")
		case "different-key":
			mustPutLevel2(t, level2BucketName, key, "value-b", "secondary")
			rawDelete = true
			deleteKeyArg = "secondary"
		case "different-bucket":
			mustPutLevel2(t, level2OtherBucket, key, "value-b", "primary")
			rawDelete = true
			deleteBucket = level2OtherBucket
		case "different-service":
			mustPutLevel2(t, level2BucketName, key, "value-b", "primary")
			rawDelete = true
		case "rapid-update":
			for _, value := range []string{"value-b", "value-c", "value-d"} {
				mustPutLevel2(t, level2BucketName, key, value, "primary")
			}
			rawDelete = true
		default:
			t.Fatalf("unknown actor action: %s", action)
		}
		if rawDelete {
			// 等待 Holder 处理摘要事件后，直接清理 Redis，不再发送失效消息。
			time.Sleep(300 * time.Millisecond)
			redisKey := serviceName + ":l2:" + string(deleteBucket) + ":" + key.RawKeyString(deleteKeyArg)
			if err := redisstarter.RawRedisClient().Del(context.Background(), redisKey).Err(); err != nil {
				t.Fatalf("delete Redis verification key: %v", err)
			}
		}
	default:
		t.Fatalf("unknown child role: %s", os.Getenv(level2RoleEnv))
	}
}

func mustPutLevel2(t *testing.T, bucketName cachecloud.BucketName, key cachecloud.CacheKey, value, keyArg string) {
	t.Helper()
	if err := cachecloud.Put(bucketName, key, value, keyArg); err != nil {
		t.Fatalf("put level2 value: %v", err)
	}
}

func readLevel2Value(t *testing.T, bucketName cachecloud.BucketName, key cachecloud.CacheKey, keyArg string) string {
	t.Helper()
	var value string
	err := cachecloud.Get(bucketName, key, &value, keyArg)
	switch {
	case err == nil:
		return "hit:" + value
	case errors.Is(err, cachecloud.ErrCacheMiss):
		return "miss"
	default:
		t.Fatalf("get level2 value: %v", err)
		return ""
	}
}
