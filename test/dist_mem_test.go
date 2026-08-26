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

	distMemConcurrentChildEnv    = "CLOUD_CACHE_DIST_MEM_CONCURRENT_CHILD"
	distMemConcurrentRoleEnv     = "CLOUD_CACHE_DIST_MEM_CONCURRENT_ROLE"
	distMemConcurrentScenarioEnv = "CLOUD_CACHE_DIST_MEM_CONCURRENT_SCENARIO"
)

type distMemComplexValue struct {
	Name       string
	Attributes map[string]int
	Tags       []string
}

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
		{name: "same value refreshes remote expiration", action: "same-refresh", wantResult: "primary=hit:value-a;secondary=miss;other=miss"},
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

func TestDistributedMemoryConcurrentSync(t *testing.T) {
	if os.Getenv(distMemConcurrentChildEnv) == "1" {
		runDistMemConcurrentChild(t)
		return
	}

	tests := []struct {
		name       string
		scenario   string
		roles      []string
		wantByRole map[string]string
	}{
		{
			name:       "cacheable nodes keep concurrently loaded same value",
			scenario:   "cacheable",
			roles:      []string{"worker", "worker", "worker"},
			wantByRole: map[string]string{"worker": "hit:cacheable-value"},
		},
		{
			name:       "concurrent updaters keep same value",
			scenario:   "updaters-same",
			roles:      []string{"holder", "holder", "updater", "updater"},
			wantByRole: map[string]string{"holder": "hit:value-a"},
		},
		{
			name:       "concurrent updaters evict different values",
			scenario:   "updaters-different",
			roles:      []string{"holder", "holder", "updater", "updater"},
			wantByRole: map[string]string{"holder": "miss"},
		},
		{
			name:       "complex map value remains consistent across nodes",
			scenario:   "complex-map",
			roles:      []string{"worker", "worker", "worker"},
			wantByRole: map[string]string{"worker": "hit:complex-value"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runDistMemConcurrentScenario(t, test.scenario, test.roles, test.wantByRole)
		})
	}
}

func runDistMemConcurrentScenario(t *testing.T, scenario string, roles []string, wantByRole map[string]string) {
	t.Helper()
	workDir := t.TempDir()
	binary, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test binary: %v", err)
	}

	commands := make([]*exec.Cmd, 0, len(roles))
	outputs := make([]strings.Builder, len(roles))
	for i, role := range roles {
		nodeName := fmt.Sprintf("%s-%d", role, i+1)
		command := exec.Command(binary, "-test.run=^TestDistributedMemoryConcurrentSync$")
		command.Env = append(os.Environ(),
			distMemConcurrentChildEnv+"=1",
			distMemConcurrentRoleEnv+"="+role,
			distMemConcurrentScenarioEnv+"="+scenario,
			distMemNodeEnv+"="+nodeName,
			distMemWorkDirEnv+"="+workDir,
		)
		command.Stdout = &outputs[i]
		command.Stderr = &outputs[i]
		if err := command.Start(); err != nil {
			t.Fatalf("start %s: %v", nodeName, err)
		}
		commands = append(commands, command)
	}
	defer func() {
		for _, command := range commands {
			if command.ProcessState == nil {
				_ = command.Process.Kill()
				_ = command.Wait()
			}
		}
	}()

	for i, role := range roles {
		readyFile := filepath.Join(workDir, fmt.Sprintf("ready-%s-%d", role, i+1))
		if !waitForFile(readyFile, 5*time.Second) {
			t.Fatalf("node %s-%d did not become ready\n%s", role, i+1, outputs[i].String())
		}
	}
	if err := os.WriteFile(filepath.Join(workDir, "start"), nil, 0o600); err != nil {
		t.Fatalf("start concurrent scenario: %v", err)
	}
	for i, role := range roles {
		doneFile := filepath.Join(workDir, fmt.Sprintf("done-%s-%d", role, i+1))
		if !waitForFile(doneFile, 10*time.Second) {
			t.Fatalf("node %s-%d did not finish action\n%s", role, i+1, outputs[i].String())
		}
	}

	// 等待所有节点处理并发操作产生的 Pub/Sub 消息后再统一验证最终状态。
	time.Sleep(500 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(workDir, "verify"), nil, 0o600); err != nil {
		t.Fatalf("signal concurrent verification: %v", err)
	}
	for i, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatalf("node %s-%d failed: %v\n%s", roles[i], i+1, err, outputs[i].String())
		}
		want, checked := wantByRole[roles[i]]
		if !checked {
			continue
		}
		result, err := os.ReadFile(filepath.Join(workDir, fmt.Sprintf("result-%s-%d", roles[i], i+1)))
		if err != nil {
			t.Fatalf("read node %s-%d result: %v", roles[i], i+1, err)
		}
		if string(result) != want {
			t.Fatalf("unexpected node %s-%d result: got %q, want %q\n%s", roles[i], i+1, result, want, outputs[i].String())
		}
	}
}

func runDistMemConcurrentChild(t *testing.T) {
	startRedis(t)
	scenario := os.Getenv(distMemConcurrentScenarioEnv)
	role := os.Getenv(distMemConcurrentRoleEnv)
	nodeName := os.Getenv(distMemNodeEnv)
	workDir := os.Getenv(distMemWorkDirEnv)
	if err := cachecloud.Init(
		cachecloud.Options{ServiceName: distMemServiceName + "-" + scenario},
		cachecloud.NewDistMemBucketConfig(distMemBucketName, time.Minute),
	); err != nil {
		t.Fatalf("init concurrent distributed memory cache: %v", err)
	}
	key := cachecloud.NewCacheKey("concurrent-key")

	if role == "holder" {
		if err := cachecloud.Put(distMemBucketName, key, "value-a"); err != nil {
			t.Fatalf("holder put initial value: %v", err)
		}
	}
	// 确保每个独立进程均已建立 Redis 订阅，再同时触发缓存操作。
	time.Sleep(300 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(workDir, "ready-"+nodeName), nil, 0o600); err != nil {
		t.Fatalf("write ready signal: %v", err)
	}
	if !waitForFile(filepath.Join(workDir, "start"), 10*time.Second) {
		t.Fatal("wait for concurrent start timeout")
	}

	switch scenario {
	case "cacheable":
		var value string
		if err := cachecloud.Cacheable(distMemBucketName, key, &value, func(result *string) (bool, error) {
			*result = "cacheable-value"
			return true, nil
		}); err != nil {
			t.Fatalf("concurrent cacheable: %v", err)
		}
	case "updaters-same":
		if role == "updater" {
			if err := cachecloud.Put(distMemBucketName, key, "value-a"); err != nil {
				t.Fatalf("concurrent updater put same value: %v", err)
			}
		}
	case "updaters-different":
		if role == "updater" {
			value := "value-b"
			if strings.HasSuffix(nodeName, "4") {
				value = "value-c"
			}
			if err := cachecloud.Put(distMemBucketName, key, value); err != nil {
				t.Fatalf("concurrent updater put different value: %v", err)
			}
		}
	case "complex-map":
		value := newDistMemComplexValue()
		if err := cachecloud.Put(distMemBucketName, key, value); err != nil {
			t.Fatalf("put complex map value: %v", err)
		}
	default:
		t.Fatalf("unknown concurrent scenario: %s", scenario)
	}

	if err := os.WriteFile(filepath.Join(workDir, "done-"+nodeName), nil, 0o600); err != nil {
		t.Fatalf("write done signal: %v", err)
	}
	if !waitForFile(filepath.Join(workDir, "verify"), 10*time.Second) {
		t.Fatal("wait for concurrent verification timeout")
	}
	result := readDistMemConcurrentResult(t, scenario, key)
	if err := os.WriteFile(filepath.Join(workDir, "result-"+nodeName), []byte(result), 0o600); err != nil {
		t.Fatalf("write concurrent result: %v", err)
	}
}

func newDistMemComplexValue() distMemComplexValue {
	return distMemComplexValue{
		Name: "complex-value",
		Attributes: map[string]int{
			"alpha": 1,
			"beta":  2,
			"gamma": 3,
			"delta": 4,
		},
		Tags: []string{"one", "two", "three"},
	}
}

func readDistMemConcurrentResult(t *testing.T, scenario string, key cachecloud.CacheKey) string {
	t.Helper()
	if scenario != "complex-map" {
		var value string
		err := cachecloud.Get(distMemBucketName, key, &value)
		switch {
		case err == nil:
			return "hit:" + value
		case errors.Is(err, cachecloud.ErrCacheMiss):
			return "miss"
		default:
			t.Fatalf("get concurrent distributed memory value: %v", err)
			return ""
		}
	}
	var value distMemComplexValue
	if err := cachecloud.Get(distMemBucketName, key, &value); err != nil {
		if errors.Is(err, cachecloud.ErrCacheMiss) {
			return "miss"
		}
		t.Fatalf("get complex map value: %v", err)
	}
	want := newDistMemComplexValue()
	if value.Name != want.Name || strings.Join(value.Tags, ",") != strings.Join(want.Tags, ",") || len(value.Attributes) != len(want.Attributes) {
		return "invalid"
	}
	for key, wantValue := range want.Attributes {
		if value.Attributes[key] != wantValue {
			return "invalid"
		}
	}
	return "hit:complex-value"
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
	holderAction := action
	if action == "different-service" {
		holderAction = ""
	}
	for i := range 2 {
		nodeName := fmt.Sprintf("holder-%d", i+1)
		holder := exec.Command(binary, "-test.run=^TestDistributedMemorySync$")
		holder.Env = append(os.Environ(),
			distMemChildEnv+"=1",
			distMemRoleEnv+"=holder",
			distMemNodeEnv+"="+nodeName,
			distMemActionEnv+"="+holderAction,
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
	if action == "same-refresh" {
		// 让 Holder 的原始 TTL 与更新节点的新 TTL 拉开超过 3 秒，触发静默续期。
		time.Sleep(4 * time.Second)
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

	// 短 TTL 场景等待原始缓存过期，以真实命中结果证明远端续期生效。
	if action == "same-refresh" {
		time.Sleep(3 * time.Second)
	} else {
		time.Sleep(300 * time.Millisecond)
	}
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
	expiration := time.Minute
	if action == "same-refresh" {
		expiration = 6 * time.Second
	}
	serviceName := distMemServiceName
	if action == "different-service" {
		serviceName += "-isolated"
	}
	if err := cachecloud.Init(
		cachecloud.Options{ServiceName: serviceName},
		cachecloud.NewDistMemBucketConfig(distMemBucketName, expiration),
		cachecloud.NewDistMemBucketConfig(distMemOtherBucket, expiration),
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
		case "same-refresh":
			if err := cachecloud.Put(distMemBucketName, key, "value-a", "primary"); err != nil {
				t.Fatalf("updater refresh same value: %v", err)
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
