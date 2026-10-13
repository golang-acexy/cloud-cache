package stablehash

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type record struct {
	ID         uint64
	Name       string
	Revision   int `json:"-"`
	Attributes map[string]any
	Scores     map[int]map[string]float64
	Tags       []string
	Created    time.Time
	Amount     decimal.Decimal
	Optional   sql.NullString
	Token      uuid.UUID
}

type customJSON struct{ Value int }

func (customJSON) MarshalJSON() ([]byte, error) { return []byte(`"hidden"`), nil }

type code string

func digest(t *testing.T, v any) string {
	t.Helper()
	s, err := Sum(v)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func fixture(seed int64) record {
	r := rand.New(rand.NewSource(seed))
	m := make(map[string]any)
	keys := []string{"nested", "bytes", "nil", "list", "number"}
	r.Shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })
	for _, key := range keys {
		switch key {
		case "nested":
			nested := make(map[int]string)
			for _, i := range r.Perm(6) {
				nested[i] = fmt.Sprint(i)
			}
			m[key] = nested
		case "bytes":
			m[key] = []byte{0, 1, 2, 255}
		case "nil":
			m[key] = (*int)(nil)
		case "list":
			m[key] = []any{"a", int64(7), true}
		case "number":
			m[key] = json.Number("123.4500")
		}
	}
	return record{ID: 42, Name: "测试/cache", Revision: 3, Attributes: m, Scores: map[int]map[string]float64{2: {"a": 1.5, "b": math.Inf(1)}, 1: {"c": math.Copysign(0, -1)}}, Tags: []string{"x", "y"}, Created: time.Date(2500, 1, 2, 3, 4, 5, 123, time.UTC), Amount: decimal.RequireFromString("1234567890.0012300"), Optional: sql.NullString{String: "present", Valid: true}, Token: uuid.MustParse("00112233-4455-6677-8899-aabbccddeeff")}
}

func TestEquivalentValues(t *testing.T) {
	integer := 123
	now := time.Now()
	f1 := new(big.Float).SetPrec(64).SetFloat64(1.25)
	f2 := new(big.Float).SetPrec(256).SetFloat64(1.25)
	shared := &struct{ Value int }{7}
	pairs := []struct {
		name string
		a, b any
	}{
		{"map order", fixture(1), fixture(2)},
		{"pointer", integer, &integer},
		{"pointer depth", &integer, func() any { p := &integer; return &p }()},
		{"float zero", float64(0), math.Copysign(0, -1)},
		{"float NaN", math.Float64frombits(0x7ff8000000000001), math.Float64frombits(0xfff0000000000001)},
		{"complex zero", complex(0.0, 0.0), complex(math.Copysign(0, -1), 0)},
		{"time zone", now, now.In(time.FixedZone("other", 8*3600))},
		{"time monotonic", now, now.Round(0)},
		{"decimal scale", decimal.RequireFromString("1.2300"), decimal.RequireFromString("123e-2")},
		{"decimal zero", decimal.New(0, math.MaxInt32), decimal.Zero},
		{"decimal huge exponent", decimal.New(100, math.MaxInt32-2), decimal.New(1, math.MaxInt32)},
		{"big integer", big.NewInt(123), func() any { return *big.NewInt(123) }()},
		{"rational reduced", big.NewRat(2, 4), big.NewRat(1, 2)},
		{"float precision", f1, f2},
		{"big float zero", new(big.Float).SetFloat64(math.Copysign(0, -1)), new(big.Float)},
		{"JSON number", json.Number("1.2300e2"), json.Number("123")},
		{"IP representations", net.IP{127, 0, 0, 1}, net.ParseIP("127.0.0.1")},
		{"address mapped", netip.MustParseAddr("::ffff:127.0.0.1"), netip.MustParseAddr("127.0.0.1")},
		{"shared references", []*struct{ Value int }{shared, shared}, []*struct{ Value int }{{7}, {7}}},
		{"tags ignored", struct {
			Value int `json:"-"`
		}{1}, struct {
			Value int `json:"shown"`
		}{1}},
		{"interface map keys", map[any]string{1: "integer", "1": "string"}, map[any]string{"1": "string", 1: "integer"}},
		{"array keys", map[[2]int]string{{1, 2}: "a", {3, 4}: "b"}, map[[2]int]string{{3, 4}: "b", {1, 2}: "a"}},
	}
	for _, pair := range pairs {
		t.Run(pair.name, func(t *testing.T) {
			a, err := canonical(pair.a)
			if err != nil {
				t.Fatal(err)
			}
			b, err := canonical(pair.b)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(a, b) {
				t.Fatalf("canonical representations differ: %x / %x", a, b)
			}
			if digest(t, pair.a) != digest(t, pair.b) {
				t.Fatal("digests differ")
			}
		})
	}
}

func TestDistinctValues(t *testing.T) {
	pairs := []struct {
		name string
		a, b any
	}{
		{"field hidden by JSON", record{Revision: 1}, record{Revision: 2}},
		{"custom JSON ignored", customJSON{1}, customJSON{2}},
		{"string boundaries", []string{"a", "bc"}, []string{"ab", "c"}},
		{"map boundaries", map[string]string{"a": "bc"}, map[string]string{"ab": "c"}},
		{"slice order", []int{1, 2}, []int{2, 1}},
		{"nil slice", []int(nil), []int{}},
		{"nil map", map[string]int(nil), map[string]int{}},
		{"nil interface", nil, (*int)(nil)},
		{"typed nil", (*int)(nil), (*string)(nil)},
		{"number type", int(1), int64(1)},
		{"named type", code("x"), "x"},
		{"container type", []int{1}, [1]int{1}},
		{"pointer field type", struct{ V int }{1}, struct{ V *int }{new(int)}},
		{"signedness", int64(1), uint64(1)},
		{"float width", float32(1.5), float64(1.5)},
		{"infinity", math.Inf(1), math.Inf(-1)},
		{"complex imaginary", complex(1, 2), complex(1, 3)},
		{"time nanoseconds", time.Unix(1, 1), time.Unix(1, 2)},
		{"decimal fraction", decimal.RequireFromString("1.23"), decimal.RequireFromString("1.2301")},
		{"decimal type", decimal.New(1, 0), json.Number("1")},
		{"rational", big.NewRat(1, 2), big.NewRat(1, 3)},
		{"float value", big.NewFloat(1.25), big.NewFloat(1.5)},
		{"duration type", time.Second, int64(time.Second)},
		{"SQL validity", sql.NullString{String: "x", Valid: true}, sql.NullString{String: "x", Valid: false}},
		{"address zone", netip.MustParseAddr("fe80::1%en0"), netip.MustParseAddr("fe80::1%en1")},
		{"raw JSON bytes", json.RawMessage(`{"a":1}`), json.RawMessage(`{ "a":1 }`)},
	}
	for _, pair := range pairs {
		t.Run(pair.name, func(t *testing.T) {
			if digest(t, pair.a) == digest(t, pair.b) {
				t.Fatalf("different values share digest: %#v / %#v", pair.a, pair.b)
			}
		})
	}
}

func TestMapPermutationAndMutation(t *testing.T) {
	expected := digest(t, fixture(0))
	for i := range 300 {
		value := fixture(int64(i))
		if digest(t, value) != expected {
			t.Fatalf("map order changed hash: %d", i)
		}
		value.Attributes["nested"].(map[int]string)[i%6] = "changed"
		if digest(t, value) == expected {
			t.Fatalf("nested map mutation missed: %d", i)
		}
	}
}

func TestNormalizedMapKeyTies(t *testing.T) {
	a, b := map[float64]string{}, map[float64]string{}
	a[math.Float64frombits(0x7ff8000000000001)] = "first"
	a[math.Float64frombits(0x7ff8000000000002)] = "second"
	b[math.NaN()] = "second"
	b[math.NaN()] = "first"
	want := digest(t, a)
	for range 100 {
		if digest(t, b) != want {
			t.Fatal("NaN-key ordering is unstable")
		}
	}
	b[math.NaN()] = "third"
	if digest(t, b) == want {
		t.Fatal("map entry count was ignored")
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m := map[time.Time]string{now: "a", now.In(time.FixedZone("zone", 3600)): "b"}
	for range 30 {
		if digest(t, m) != digest(t, m) {
			t.Fatal("normalized time keys are unstable")
		}
	}
}

func TestUnsupportedAndInvalidValues(t *testing.T) {
	type hidden struct {
		Visible int
		hidden  int
	}
	pointer := 1
	for _, value := range []any{func() {}, make(chan int), (chan int)(nil), uintptr(1), unsafe.Pointer(&pointer), hidden{}, map[*int]int{}, map[any]int{&pointer: 1}, map[struct{ P *int }]int{{&pointer}: 1}, map[[1]any]int{{&pointer}: 1}} {
		sum, err := Sum(value)
		if !errors.Is(err, ErrUnsupported) || sum != "" {
			t.Fatalf("%T: digest=%s err=%v", value, sum, err)
		}
	}
	for _, value := range []any{net.IP{}, net.IP{1, 2}, json.Number(""), json.Number("01"), json.Number(" 1"), json.Number("null"), json.Number("NaN"), json.Number("1e999999999999999999999")} {
		if sum, err := Sum(value); !errors.Is(err, ErrInvalid) || sum != "" {
			t.Fatalf("%T: %s %v", value, sum, err)
		}
	}
}

func TestCyclesAndLimits(t *testing.T) {
	type node struct{ Next *node }
	n := &node{}
	n.Next = n
	m := map[string]any{}
	m["self"] = m
	s := make([]any, 1)
	s[0] = s
	for _, value := range []any{n, m, s} {
		if sum, err := Sum(value); !errors.Is(err, ErrCycle) || sum != "" {
			t.Fatalf("cycle %T: %s %v", value, sum, err)
		}
	}
	var deep any = 1
	for range maxDepth + 1 {
		deep = []any{deep}
	}
	for _, value := range []any{deep, make([]int, maxNodes+1), make([]byte, maxWorkBytes+1)} {
		if sum, err := Sum(value); !errors.Is(err, ErrLimit) || sum != "" {
			t.Fatalf("limit %T: %s %v", value, sum, err)
		}
	}
}

func TestConcurrentReadOnlyCalls(t *testing.T) {
	value := fixture(10)
	want := digest(t, value)
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 25 {
				got, err := Sum(value)
				if err != nil || got != want {
					t.Errorf("concurrent digest: %s %v", got, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestCrossProcessStability(t *testing.T) {
	if os.Getenv("STABLEHASH_CHILD") == "1" {
		// Vary Gob's process-local type registration; the prototype must not depend on it.
		if os.Getenv("STABLEHASH_ORDER") == "reverse" {
			var b bytes.Buffer
			_ = gob.NewEncoder(&b).Encode(struct{ Other []int }{[]int{1, 2}})
		}
		s, err := Sum(fixture(time.Now().UnixNano()))
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("DIGEST=" + s)
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	want := digest(t, fixture(0))
	for i := range 4 {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, exe, "-test.run=^TestCrossProcessStability$", "-test.timeout=8s")
		order := "normal"
		if i%2 == 1 {
			order = "reverse"
		}
		cmd.Env = append(os.Environ(), "STABLEHASH_CHILD=1", "STABLEHASH_ORDER="+order)
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("child: %v\n%s", err, out)
		}
		if !strings.Contains(string(out), "DIGEST="+want+"\n") {
			t.Fatalf("cross-process mismatch: %s", out)
		}
	}
}

func TestSupportedIntegrationTypes(t *testing.T) {
	values := []any{
		time.Time{}, time.Duration(-10), decimal.Decimal{}, big.Int{}, big.Rat{}, big.Float{},
		net.IP(nil), netip.Addr{}, netip.MustParseAddr("2001:db8::1"),
		sql.NullBool{Bool: true, Valid: true}, sql.NullInt64{Int64: 10, Valid: true}, sql.NullFloat64{Float64: 1.25, Valid: true}, sql.NullTime{Time: time.Unix(0, 1), Valid: true},
		sql.Null[int]{V: 1, Valid: true}, uuid.New(), json.RawMessage(`{"x":1}`),
		map[bool]string{true: "yes", false: "no"}, map[struct {
			ID   int
			Name string
		}]int{{1, "x"}: 2},
	}
	for _, value := range values {
		t.Run(reflect.TypeOf(value).String(), func(t *testing.T) {
			if len(digest(t, value)) != 32 {
				t.Fatal("unexpected digest length")
			}
		})
	}
}

func TestDeepDynamicMapKeysRespectLimits(t *testing.T) {
	type key struct{ Next any }
	var value any = 1
	for range maxDepth + 2 {
		value = key{Next: value}
	}
	sum, err := Sum(map[any]int{value: 1})
	if sum != "" || !errors.Is(err, ErrLimit) {
		t.Fatalf("deep map key: %s %v", sum, err)
	}
}

func TestSpecialValueBoundaries(t *testing.T) {
	equal := [][2]any{
		{json.Number("-0.00"), json.Number("0")},
		{decimal.New(-1200, -2), decimal.New(-12, 0)},
		{new(big.Float).SetInf(false), new(big.Float).SetPrec(300).SetInf(false)},
		{time.Date(1200, 1, 1, 0, 0, 0, 1, time.UTC), time.Date(1200, 1, 1, 1, 0, 0, 1, time.FixedZone("offset", 3600))},
	}
	for _, pair := range equal {
		if digest(t, pair[0]) != digest(t, pair[1]) {
			t.Fatalf("special equality: %T", pair[0])
		}
	}
	if digest(t, new(big.Float).SetInf(false)) == digest(t, new(big.Float).SetInf(true)) {
		t.Fatal("infinity signs collapsed")
	}
}
