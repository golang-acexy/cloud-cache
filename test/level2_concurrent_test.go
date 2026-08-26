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
	level2ConcurrentChildEnv    = "CLOUD_CACHE_LEVEL2_CONCURRENT_CHILD"
	level2ConcurrentRoleEnv     = "CLOUD_CACHE_LEVEL2_CONCURRENT_ROLE"
	level2ConcurrentScenarioEnv = "CLOUD_CACHE_LEVEL2_CONCURRENT_SCENARIO"
	level2ConcurrentServiceEnv  = "CLOUD_CACHE_LEVEL2_CONCURRENT_SERVICE"
	level2ConcurrentWorkDirEnv  = "CLOUD_CACHE_LEVEL2_CONCURRENT_WORK_DIR"
	level2ConcurrentNodeEnv     = "CLOUD_CACHE_LEVEL2_CONCURRENT_NODE"

	level2ConcurrentBucket = cachecloud.BucketName("level2-concurrent")
)

type level2ComplexValue struct {
	Name       string
	Attributes map[string]int
	Groups     map[string]map[int]string
	Tags       []string
}

func TestLevel2ConcurrentSync(t *testing.T) {
	if os.Getenv(level2ConcurrentChildEnv) == "1" {
		runLevel2ConcurrentChild(t)
		return
	}

	tests := []struct {
		name       string
		scenario   string
		roles      []string
		wantByRole map[string]string
	}{
		{
			name:       "cacheable nodes converge on shared Redis value",
			scenario:   "cacheable",
			roles:      []string{"worker", "worker", "worker"},
			wantByRole: map[string]string{"worker": "hit:cacheable-value"},
		},
		{
			name:       "concurrent updaters keep same level1 value",
			scenario:   "updaters-same",
			roles:      []string{"holder", "holder", "updater", "updater"},
			wantByRole: map[string]string{"holder": "hit:value-a"},
		},
		{
			name:       "concurrent different updates converge to Redis winner",
			scenario:   "updaters-different",
			roles:      []string{"holder", "holder", "updater", "updater"},
			wantByRole: map[string]string{},
		},
		{
			name:       "complex map value remains in level1 after Redis deletion",
			scenario:   "complex-map",
			roles:      []string{"holder", "holder", "updater"},
			wantByRole: map[string]string{"holder": "hit:complex-value"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runLevel2ConcurrentScenario(t, test.scenario, test.roles, test.wantByRole)
		})
	}
}

func runLevel2ConcurrentScenario(t *testing.T, scenario string, roles []string, wantByRole map[string]string) {
	t.Helper()
	workDir := t.TempDir()
	binary, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test binary: %v", err)
	}
	serviceName := fmt.Sprintf("cloud-cache-level2-%s-%d", scenario, time.Now().UnixNano())

	commands := make([]*exec.Cmd, 0, len(roles))
	outputs := make([]strings.Builder, len(roles))
	for i, role := range roles {
		nodeName := fmt.Sprintf("%s-%d", role, i+1)
		command := exec.Command(binary, "-test.run=^TestLevel2ConcurrentSync$")
		command.Env = append(os.Environ(),
			level2ConcurrentChildEnv+"=1",
			level2ConcurrentRoleEnv+"="+role,
			level2ConcurrentScenarioEnv+"="+scenario,
			level2ConcurrentServiceEnv+"="+serviceName,
			level2ConcurrentWorkDirEnv+"="+workDir,
			level2ConcurrentNodeEnv+"="+nodeName,
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
		t.Fatalf("start concurrent L2 scenario: %v", err)
	}
	for i, role := range roles {
		doneFile := filepath.Join(workDir, fmt.Sprintf("done-%s-%d", role, i+1))
		if !waitForFile(doneFile, 10*time.Second) {
			t.Fatalf("node %s-%d did not finish action\n%s", role, i+1, outputs[i].String())
		}
	}

	time.Sleep(500 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(workDir, "verify"), nil, 0o600); err != nil {
		t.Fatalf("signal concurrent L2 verification: %v", err)
	}
	holderResults := make([]string, 0, 2)
	for i, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatalf("node %s-%d failed: %v\n%s", roles[i], i+1, err, outputs[i].String())
		}
		result, err := os.ReadFile(filepath.Join(workDir, fmt.Sprintf("result-%s-%d", roles[i], i+1)))
		if err != nil {
			t.Fatalf("read node %s-%d result: %v", roles[i], i+1, err)
		}
		if roles[i] == "holder" {
			holderResults = append(holderResults, string(result))
		}
		if want, checked := wantByRole[roles[i]]; checked && string(result) != want {
			t.Fatalf("unexpected node %s-%d result: got %q, want %q\n%s", roles[i], i+1, result, want, outputs[i].String())
		}
	}
	if scenario == "updaters-different" {
		assertLevel2ConcurrentWinner(t, holderResults)
	}
}

func assertLevel2ConcurrentWinner(t *testing.T, holderResults []string) {
	t.Helper()
	if len(holderResults) != 2 {
		t.Fatalf("expected two holder results, got %d", len(holderResults))
	}
	if holderResults[0] != holderResults[1] {
		t.Fatalf("holders did not converge to the same Redis value: %q and %q", holderResults[0], holderResults[1])
	}
	if holderResults[0] != "hit:value-b" && holderResults[0] != "hit:value-c" {
		t.Fatalf("holders converged to an unexpected value: %q", holderResults[0])
	}
}

func runLevel2ConcurrentChild(t *testing.T) {
	startRedis(t)
	role := os.Getenv(level2ConcurrentRoleEnv)
	scenario := os.Getenv(level2ConcurrentScenarioEnv)
	serviceName := os.Getenv(level2ConcurrentServiceEnv)
	workDir := os.Getenv(level2ConcurrentWorkDirEnv)
	nodeName := os.Getenv(level2ConcurrentNodeEnv)
	if err := cachecloud.Init(
		cachecloud.Options{ServiceName: serviceName},
		cachecloud.NewLevel2BucketConfig(level2ConcurrentBucket, time.Minute, time.Minute),
	); err != nil {
		t.Fatalf("init concurrent L2 cache: %v", err)
	}
	key := cachecloud.NewCacheKey("concurrent-key")

	if role == "holder" {
		switch scenario {
		case "updaters-same", "updaters-different":
			if err := cachecloud.Put(level2ConcurrentBucket, key, "value-a"); err != nil {
				t.Fatalf("holder put initial value: %v", err)
			}
		case "complex-map":
			if err := cachecloud.Put(level2ConcurrentBucket, key, newLevel2ComplexValue()); err != nil {
				t.Fatalf("holder put initial complex value: %v", err)
			}
		}
	}
	time.Sleep(300 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(workDir, "ready-"+nodeName), nil, 0o600); err != nil {
		t.Fatalf("write L2 ready signal: %v", err)
	}
	if !waitForFile(filepath.Join(workDir, "start"), 10*time.Second) {
		t.Fatal("wait for concurrent L2 start timeout")
	}

	switch scenario {
	case "cacheable":
		var value string
		if err := cachecloud.Cacheable(level2ConcurrentBucket, key, &value, func(result *string) (bool, error) {
			*result = "cacheable-value"
			return true, nil
		}); err != nil {
			t.Fatalf("concurrent L2 cacheable: %v", err)
		}
	case "updaters-same":
		if role == "updater" {
			if err := cachecloud.Put(level2ConcurrentBucket, key, "value-a"); err != nil {
				t.Fatalf("concurrent L2 updater put same value: %v", err)
			}
		}
	case "updaters-different":
		if role == "updater" {
			value := "value-b"
			if strings.HasSuffix(nodeName, "4") {
				value = "value-c"
			}
			if err := cachecloud.Put(level2ConcurrentBucket, key, value); err != nil {
				t.Fatalf("concurrent L2 updater put different value: %v", err)
			}
		}
	case "complex-map":
		if role == "updater" {
			if err := cachecloud.Put(level2ConcurrentBucket, key, newLevel2ComplexValue()); err != nil {
				t.Fatalf("concurrent L2 updater put complex value: %v", err)
			}
			time.Sleep(300 * time.Millisecond)
			redisKey := serviceName + ":l2:" + string(level2ConcurrentBucket) + ":" + key.RawKeyString()
			if err := redisstarter.RawRedisClient().Del(context.Background(), redisKey).Err(); err != nil {
				t.Fatalf("delete complex Redis verification key: %v", err)
			}
		}
	default:
		t.Fatalf("unknown concurrent L2 scenario: %s", scenario)
	}

	if err := os.WriteFile(filepath.Join(workDir, "done-"+nodeName), nil, 0o600); err != nil {
		t.Fatalf("write L2 done signal: %v", err)
	}
	if !waitForFile(filepath.Join(workDir, "verify"), 10*time.Second) {
		t.Fatal("wait for concurrent L2 verification timeout")
	}
	result := readLevel2ConcurrentResult(t, scenario, key)
	if err := os.WriteFile(filepath.Join(workDir, "result-"+nodeName), []byte(result), 0o600); err != nil {
		t.Fatalf("write concurrent L2 result: %v", err)
	}
}

func newLevel2ComplexValue() level2ComplexValue {
	return level2ComplexValue{
		Name:       "complex-value",
		Attributes: map[string]int{"alpha": 1, "beta": 2, "gamma": 3},
		Groups: map[string]map[int]string{
			"first":  {1: "one", 2: "two"},
			"second": {3: "three", 4: "four"},
		},
		Tags: []string{"one", "two", "three"},
	}
}

func readLevel2ConcurrentResult(t *testing.T, scenario string, key cachecloud.CacheKey) string {
	t.Helper()
	if scenario != "complex-map" {
		var value string
		err := cachecloud.Get(level2ConcurrentBucket, key, &value)
		switch {
		case err == nil:
			return "hit:" + value
		case errors.Is(err, cachecloud.ErrCacheMiss):
			return "miss"
		default:
			t.Fatalf("get concurrent L2 value: %v", err)
			return ""
		}
	}
	var value level2ComplexValue
	if err := cachecloud.Get(level2ConcurrentBucket, key, &value); err != nil {
		if errors.Is(err, cachecloud.ErrCacheMiss) {
			return "miss"
		}
		t.Fatalf("get concurrent L2 complex value: %v", err)
	}
	want := newLevel2ComplexValue()
	if value.Name != want.Name || strings.Join(value.Tags, ",") != strings.Join(want.Tags, ",") || len(value.Attributes) != len(want.Attributes) || len(value.Groups) != len(want.Groups) {
		return "invalid"
	}
	for key, wantValue := range want.Attributes {
		if value.Attributes[key] != wantValue {
			return "invalid"
		}
	}
	for group, wantValues := range want.Groups {
		for key, wantValue := range wantValues {
			if value.Groups[group][key] != wantValue {
				return "invalid"
			}
		}
	}
	return "hit:complex-value"
}
