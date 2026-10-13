package stablehash

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"testing"
)

func FuzzCanonicalMapOrder(f *testing.F) {
	f.Add([]byte("nested maps with different insertion order"))
	f.Add([]byte{0, 0, 1, 255, 2, 128, 3, 3, 4, 0})
	f.Add([]byte("a:bc\x00ab:c测试"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 2048 {
			return
		}
		first, second := map[string]any{}, map[string]any{}
		for i, b := range data {
			key := fmt.Sprint(i)
			var value any
			switch b % 5 {
			case 0:
				value = []byte{b, byte(i)}
			case 1:
				value = map[int]string{int(b): string(data[:i]), int(b) + 1: string(data[i:])}
			case 2:
				var bits [8]byte
				copy(bits[:], data[i:])
				value = math.Float64frombits(binary.BigEndian.Uint64(bits[:]))
			case 3:
				value = []any{int(b), nil, string([]byte{b})}
			case 4:
				value = (*int)(nil)
			}
			first[key] = value
		}
		for i := len(data) - 1; i >= 0; i-- {
			key := fmt.Sprint(i)
			second[key] = first[key]
		}
		a, err := canonical(first)
		if err != nil {
			t.Fatal(err)
		}
		b, err := canonical(second)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a, b) {
			t.Fatal("insertion order changed canonical bytes")
		}
		if digest(t, first) != digest(t, second) {
			t.Fatal("map digests differ")
		}
		second["mutation"] = true
		if digest(t, first) == digest(t, second) {
			t.Fatal("map addition missed")
		}
	})
}
