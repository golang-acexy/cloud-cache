package cachecloud

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/zeebo/xxh3"
)

type cacheValueBenchmarkModel struct {
	ID         uint64
	Name       string
	Payload    string
	Attributes map[string]int
	Tags       []string
}

var (
	benchmarkBytes    []byte
	benchmarkEnvelope cacheValueEnvelope
	benchmarkHash     string
)

func BenchmarkCacheValueEncoding(b *testing.B) {
	for _, size := range []int{1024, 10 * 1024, 100 * 1024, 1024 * 1024} {
		value := newCacheValueBenchmarkModel(size)
		b.Run(benchmarkSizeName(size)+"/gob", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size))
			for range b.N {
				var payload bytes.Buffer
				if err := gob.NewEncoder(&payload).Encode(value); err != nil {
					b.Fatal(err)
				}
				benchmarkBytes = payload.Bytes()
			}
		})
		b.Run(benchmarkSizeName(size)+"/json", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size))
			for range b.N {
				payload, err := json.Marshal(value)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkBytes = payload
			}
		})
	}
}

func BenchmarkCacheValueHash(b *testing.B) {
	for _, size := range []int{1024, 10 * 1024, 100 * 1024, 1024 * 1024} {
		value := newCacheValueBenchmarkModel(size)
		jsonPayload, err := json.Marshal(value)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(benchmarkSizeName(size)+"/md5-only", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(jsonPayload)))
			for range b.N {
				sum := md5.Sum(jsonPayload)
				benchmarkHash = hex.EncodeToString(sum[:])
			}
		})
		b.Run(benchmarkSizeName(size)+"/xxhash64-only", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(jsonPayload)))
			for range b.N {
				var sum [8]byte
				binary.BigEndian.PutUint64(sum[:], xxhash.Sum64(jsonPayload))
				benchmarkHash = hex.EncodeToString(sum[:])
			}
		})
		b.Run(benchmarkSizeName(size)+"/xxh3-128-only", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(jsonPayload)))
			for range b.N {
				sum128 := xxh3.Hash128(jsonPayload)
				var sum [16]byte
				binary.BigEndian.PutUint64(sum[:8], sum128.Hi)
				binary.BigEndian.PutUint64(sum[8:], sum128.Lo)
				benchmarkHash = hex.EncodeToString(sum[:])
			}
		})
		b.Run(benchmarkSizeName(size)+"/json-and-xxh3-128", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size))
			for range b.N {
				benchmarkHash = cacheValueHash(value, nil)
			}
		})
	}
}

func BenchmarkNewCacheValueEnvelope(b *testing.B) {
	for _, size := range []int{1024, 10 * 1024, 100 * 1024, 1024 * 1024} {
		value := newCacheValueBenchmarkModel(size)
		b.Run(benchmarkSizeName(size), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size))
			for range b.N {
				envelope, err := newCacheValueEnvelope(value, time.Minute)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkEnvelope = envelope
			}
		})
	}
}

func newCacheValueBenchmarkModel(payloadSize int) cacheValueBenchmarkModel {
	return cacheValueBenchmarkModel{
		ID:      1001,
		Name:    "benchmark-model",
		Payload: strings.Repeat("x", payloadSize),
		Attributes: map[string]int{
			"alpha": 1,
			"beta":  2,
			"gamma": 3,
			"delta": 4,
		},
		Tags: []string{"one", "two", "three", "four"},
	}
}

func benchmarkSizeName(size int) string {
	switch size {
	case 1024:
		return "1KB"
	case 10 * 1024:
		return "10KB"
	case 100 * 1024:
		return "100KB"
	case 1024 * 1024:
		return "1MB"
	default:
		return "unknown"
	}
}
