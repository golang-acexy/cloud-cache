package stablehash

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/zeebo/xxh3"
)

type benchmarkValue struct {
	ID         int
	Name       string
	Attributes map[string]int
	Nested     map[string][]string
	Payload    []byte
}

var benchmarkDigest string
var benchmarkRawDigest xxh3.Uint128

func BenchmarkDigest(b *testing.B) {
	for _, tc := range []struct {
		name       string
		keys, size int
	}{{"small", 8, 1024}, {"medium", 64, 16 << 10}, {"large", 1024, 256 << 10}} {
		value := benchmarkValue{ID: 1, Name: "example", Attributes: make(map[string]int), Nested: map[string][]string{"a": {"one", "two"}, "b": {"three"}}, Payload: []byte(strings.Repeat("x", tc.size))}
		for i := range tc.keys {
			value.Attributes[fmt.Sprint(i)] = i
		}
		b.Run(tc.name, func(b *testing.B) {
			b.Run("stable", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					var err error
					benchmarkDigest, err = Sum(value)
					if err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("gob-payload", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					var buf bytes.Buffer
					if err := gob.NewEncoder(&buf).Encode(value); err != nil {
						b.Fatal(err)
					}
					benchmarkRawDigest = xxh3.Hash128(buf.Bytes())
				}
			})
			b.Run("json", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					data, err := json.Marshal(value)
					if err != nil {
						b.Fatal(err)
					}
					benchmarkRawDigest = xxh3.Hash128(data)
				}
			})
			b.Run("gob-roundtrip-stable", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					var buf bytes.Buffer
					if err := gob.NewEncoder(&buf).Encode(value); err != nil {
						b.Fatal(err)
					}
					var decoded benchmarkValue
					if err := gob.NewDecoder(&buf).Decode(&decoded); err != nil {
						b.Fatal(err)
					}
					var err error
					benchmarkDigest, err = Sum(decoded)
					if err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}
