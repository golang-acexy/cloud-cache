package cachecloud

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/golang-acexy/cloud-cache/internal/stablehash"
)

type hashTestValue struct {
	Name     string
	Revision int `json:"-"`
}

func TestEnvelopeHashUsesStoredPayload(t *testing.T) {
	values := []any{hashTestValue{"value", 1}, map[string]int{"a": 1, "b": 2, "c": 3}, map[string]map[int]string{"nested": {1: "one", 2: "two"}}}
	for _, value := range values {
		for range 16 {
			envelope, err := newCacheValueEnvelope(value, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			result := reflect.New(reflect.TypeOf(value))
			if err := envelope.decode(result.Interface()); err != nil {
				t.Fatal(err)
			}
			want, err := stablehash.Sum(result.Elem().Interface())
			if err != nil || envelope.ValueHash != "stable:"+want {
				t.Fatalf("hash does not describe decoded payload: %v", err)
			}
			if !reflect.DeepEqual(result.Elem().Interface(), value) {
				t.Fatal("payload round trip changed data")
			}
		}
	}
}

func TestEnvelopeHashIncludesJSONOmittedFields(t *testing.T) {
	first, err := newCacheValueEnvelope(hashTestValue{"value", 1}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newCacheValueEnvelope(hashTestValue{"value", 2}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(hashTestValue{"value", 1})
	b, _ := json.Marshal(hashTestValue{"value", 2})
	if !bytes.Equal(a, b) {
		t.Fatal("test requires JSON-equivalent values")
	}
	if first.ValueHash == second.ValueHash {
		t.Fatal("stored field changes must change the hash")
	}
}

func TestEnvelopeHashIgnoresExpiration(t *testing.T) {
	value := hashTestValue{"same", 1}
	first, err := newCacheValueEnvelope(value, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newCacheValueEnvelope(&value, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Payload, second.Payload) || first.ValueHash != second.ValueHash {
		t.Fatal("equal scalar payloads must share a hash")
	}
	if first.ExpireAt == second.ExpireAt {
		t.Fatal("test requires different deadlines")
	}
}

func TestEnvelopeExpirationExtension(t *testing.T) {
	now := time.Unix(100, 0)
	for _, tc := range []struct {
		name                     string
		local, remote, ttl, want int64
		changed                  bool
	}{
		{"cap", 110, 150, 30, 130, true},
		{"exact tolerance", 110, 113, 30, 110, false},
		{"small advance", 110, 112, 30, 110, false},
		{"expired target", 90, 99, 30, 90, false},
		{"target equals now", 90, 100, 30, 90, false},
		{"expired local can renew", 90, 120, 30, 120, true},
		{"no shortening", 130, 120, 30, 130, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			envelope := cacheValueEnvelope{ExpireAt: tc.local * 1000}
			changed := envelope.extendExpiration(tc.remote*1000, time.Duration(tc.ttl)*time.Second, now)
			if changed != tc.changed || envelope.ExpireAt != tc.want*1000 {
				t.Fatalf("changed=%v expiration=%d", changed, envelope.ExpireAt)
			}
		})
	}
}

// storedTransform exercises a custom codec that changes the observable value.
type storedTransform struct{ Value string }

func (v storedTransform) GobEncode() ([]byte, error) { return []byte(v.Value), nil }
func (v *storedTransform) GobDecode(data []byte) error {
	v.Value = "decoded:" + string(data)
	return nil
}

func TestEnvelopeHashesDecodedValue(t *testing.T) {
	input := storedTransform{Value: "input"}
	envelope, err := newCacheValueEnvelope(input, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var output storedTransform
	if err := envelope.decode(&output); err != nil {
		t.Fatal(err)
	}
	want, err := stablehash.Sum(output)
	if err != nil {
		t.Fatal(err)
	}
	original, _ := stablehash.Sum(input)
	if envelope.ValueHash != "stable:"+want || want == original {
		t.Fatal("digest must describe the transformed stored value")
	}
}

func TestEnvelopeStableMapsAndFallback(t *testing.T) {
	var first string
	for i := range 100 {
		value := make(map[int]string)
		for j := range 20 {
			value[(i+j)%20] = "value"
		}
		envelope, err := newCacheValueEnvelope(value, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = envelope.ValueHash
		}
		if envelope.ValueHash != first {
			t.Fatal("map order changed the digest")
		}
	}
	for _, value := range []any{
		struct {
			Public  string
			private int
		}{Public: "value", private: 1},
		map[*int]string{new(int): "value"},
	} {
		envelope, err := newCacheValueEnvelope(value, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if envelope.ValueHash != "gob:"+xxh3Hex(envelope.Payload) {
			t.Fatal("unsupported values must use tagged payload fallback")
		}
	}
}
