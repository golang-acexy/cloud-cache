// Package stablehash provides deterministic, typed value hashing for cache synchronization.
// It does not invoke user codecs; callers supply the decoded storage representation.
package stablehash

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"net"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"github.com/zeebo/xxh3"
)

const (
	maxDepth     = 64
	maxNodes     = 100_000
	maxWorkBytes = 16 << 20
)

// Sum returns a 32-digit XXH3-128 digest of a canonical value representation.
// Maps are unordered; slices and arrays are ordered. Non-nil pointers and
// interfaces are transparent at value positions, but declared container and field
// types remain significant. Nil and empty containers differ. JSON tags are ignored.
// Times compare by instant; decimals and big numbers by numeric value; floating
// zeros and NaNs are normalized. See the internal design document for type rules.
// Unsupported kinds, unexported struct fields, pointer map keys, cycles, invalid
// special values, and resource limits return an error and no digest. Limits are
// 64 traversal levels, 100,000 values, and 16 MiB of cumulative encoding work.
// Concurrent calls are independent; callers must not mutate inputs during a call.
// The digest is not cryptographic and does not prove equality or authenticity.
func Sum(value any) (string, error) {
	data, err := canonical(value)
	if err != nil {
		return "", err
	}
	hash := xxh3.Hash128(data)
	var sum [16]byte
	binary.BigEndian.PutUint64(sum[:8], hash.Hi)
	binary.BigEndian.PutUint64(sum[8:], hash.Lo)
	return hex.EncodeToString(sum[:]), nil
}

type visit struct {
	typ     reflect.Type
	pointer uintptr
	length  int
}
type encoder struct {
	active      map[visit]bool
	nodes, work int
	err         error
}
type buffer struct {
	owner *encoder
	data  []byte
}

func canonical(value any) ([]byte, error) {
	e := &encoder{active: make(map[visit]bool)}
	w := &buffer{owner: e}
	w.text("cloud-cache/stable-value")
	e.value(w, reflect.ValueOf(value), 0)
	if e.err != nil {
		return nil, e.err
	}
	return w.data, nil
}

func (w *buffer) raw(data []byte) {
	if w.owner.err != nil {
		return
	}
	if len(data) > maxWorkBytes-w.owner.work {
		w.owner.err = fmt.Errorf("%w: encoding bytes", ErrLimit)
		return
	}
	w.owner.work += len(data)
	w.data = append(w.data, data...)
}
func (w *buffer) octet(b byte)    { w.raw([]byte{b}) }
func (w *buffer) number(n uint64) { var b [8]byte; binary.BigEndian.PutUint64(b[:], n); w.raw(b[:]) }
func (w *buffer) blob(b []byte)   { w.number(uint64(len(b))); w.raw(b) }
func (w *buffer) text(s string) {
	if len(s) > maxWorkBytes-w.owner.work {
		w.owner.err = fmt.Errorf("%w: text bytes", ErrLimit)
		return
	}
	w.blob([]byte(s))
}
func (e *encoder) fail(kind error, t reflect.Type) { e.err = fmt.Errorf("%w: %s", kind, t) }

func (e *encoder) value(w *buffer, v reflect.Value, depth int) {
	if e.err != nil {
		return
	}
	if depth > maxDepth || e.nodes >= maxNodes {
		e.err = fmt.Errorf("%w: traversal", ErrLimit)
		return
	}
	e.nodes++
	if !v.IsValid() {
		w.octet('0')
		return
	}
	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			w.octet('0')
			return
		}
		e.value(w, v.Elem(), depth+1)
		return
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			w.octet('N')
			e.typ(w, v.Type(), depth)
			return
		}
		if !e.enter(v) {
			return
		}
		defer e.leave(v)
		e.value(w, v.Elem(), depth+1)
		return
	}
	w.octet('V')
	e.typ(w, v.Type(), depth)
	if e.err != nil {
		return
	}
	if e.special(w, v) {
		return
	}
	switch v.Kind() {
	case reflect.Bool:
		if v.Bool() {
			w.octet(1)
		} else {
			w.octet(0)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		w.number(uint64(v.Int()))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		w.number(v.Uint())
	case reflect.Float32, reflect.Float64:
		w.number(floatBits(v.Float()))
	case reflect.Complex64, reflect.Complex128:
		c := v.Complex()
		w.number(floatBits(real(c)))
		w.number(floatBits(imag(c)))
	case reflect.String:
		w.text(v.String())
	case reflect.Slice:
		if v.IsNil() {
			w.octet(0)
			return
		}
		w.octet(1)
		if !e.enter(v) {
			return
		}
		defer e.leave(v)
		if v.Type().Elem() == reflect.TypeFor[byte]() {
			w.blob(v.Bytes())
			return
		}
		e.sequence(w, v, depth)
	case reflect.Array:
		e.sequence(w, v, depth)
	case reflect.Map:
		if !safeMapKey(v.Type().Key(), 0) {
			e.fail(ErrUnsupported, v.Type())
			return
		}
		if v.IsNil() {
			w.octet(0)
			return
		}
		w.octet(1)
		if !e.enter(v) {
			return
		}
		defer e.leave(v)
		if v.Len() > maxNodes-e.nodes {
			e.fail(ErrLimit, v.Type())
			return
		}
		entries := make([][]byte, 0, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			if !e.mapKey(iter.Key(), depth+1) {
				return
			}
			entry := &buffer{owner: e}
			e.value(entry, iter.Key(), depth+1)
			e.value(entry, iter.Value(), depth+1)
			if e.err != nil {
				return
			}
			entries = append(entries, entry.data)
		}
		// Sort the complete key/value entry, including ties between normalized keys.
		// This also makes multiple NaN keys deterministic without map lookups.
		slices.SortFunc(entries, bytes.Compare)
		w.number(uint64(len(entries)))
		for _, entry := range entries {
			w.blob(entry)
		}
	case reflect.Struct:
		w.number(uint64(v.NumField()))
		for i := range v.NumField() {
			f := v.Type().Field(i)
			if f.PkgPath != "" {
				e.fail(ErrUnsupported, v.Type())
				return
			}
			w.text(f.Name)
			e.value(w, v.Field(i), depth+1)
			if e.err != nil {
				return
			}
		}
	default:
		e.fail(ErrUnsupported, v.Type())
	}
}

func (e *encoder) sequence(w *buffer, v reflect.Value, depth int) {
	if v.Len() > maxNodes-e.nodes {
		e.fail(ErrLimit, v.Type())
		return
	}
	w.number(uint64(v.Len()))
	for i := range v.Len() {
		e.value(w, v.Index(i), depth+1)
		if e.err != nil {
			return
		}
	}
}

func reference(v reflect.Value) visit {
	length := 0
	if v.Kind() == reflect.Slice {
		length = v.Len()
	}
	return visit{v.Type(), uintptr(v.UnsafePointer()), length}
}
func (e *encoder) enter(v reflect.Value) bool {
	key := reference(v)
	if e.active[key] {
		e.fail(ErrCycle, v.Type())
		return false
	}
	e.active[key] = true
	return true
}
func (e *encoder) leave(v reflect.Value) { delete(e.active, reference(v)) }

func floatBits(f float64) uint64 {
	if f == 0 {
		return 0
	}
	if math.IsNaN(f) {
		return 0x7ff8000000000000
	}
	return math.Float64bits(f)
}

func safeMapKey(t reflect.Type, depth int) bool {
	if depth > maxDepth {
		return false
	}
	if t == reflect.TypeFor[time.Time]() || t == reflect.TypeFor[netip.Addr]() {
		return true
	}
	switch t.Kind() {
	case reflect.Pointer, reflect.UnsafePointer, reflect.Chan, reflect.Uintptr:
		return false
	case reflect.Array:
		return safeMapKey(t.Elem(), depth+1)
	case reflect.Struct:
		for i := range t.NumField() {
			if !safeMapKey(t.Field(i).Type, depth+1) {
				return false
			}
		}
	}
	return true
}
func (e *encoder) mapKey(v reflect.Value, depth int) bool {
	if depth > maxDepth || e.nodes >= maxNodes {
		e.fail(ErrLimit, v.Type())
		return false
	}
	e.nodes++
	if v.Kind() == reflect.Interface {
		return v.IsNil() || e.mapKey(v.Elem(), depth+1)
	}
	if v.Type() == reflect.TypeFor[time.Time]() || v.Type() == reflect.TypeFor[netip.Addr]() {
		return true
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.UnsafePointer, reflect.Chan, reflect.Uintptr:
		e.fail(ErrUnsupported, v.Type())
		return false
	case reflect.Array:
		for i := range v.Len() {
			if !e.mapKey(v.Index(i), depth+1) {
				return false
			}
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if !e.mapKey(v.Field(i), depth+1) {
				return false
			}
		}
	}
	return true
}

func (e *encoder) typ(w *buffer, t reflect.Type, depth int) {
	if e.err != nil {
		return
	}
	if depth > maxDepth {
		e.fail(ErrLimit, t)
		return
	}
	if t.Name() != "" {
		w.octet('n')
		w.text(t.PkgPath())
		w.text(t.Name())
		return
	}
	w.octet('k')
	w.text(t.Kind().String())
	switch t.Kind() {
	case reflect.Array:
		w.number(uint64(t.Len()))
		e.typ(w, t.Elem(), depth+1)
	case reflect.Slice, reflect.Pointer:
		e.typ(w, t.Elem(), depth+1)
	case reflect.Map:
		e.typ(w, t.Key(), depth+1)
		e.typ(w, t.Elem(), depth+1)
	case reflect.Struct:
		w.number(uint64(t.NumField()))
		for i := range t.NumField() {
			f := t.Field(i)
			w.text(f.Name)
			w.text(f.PkgPath)
			if f.Anonymous {
				w.octet(1)
			} else {
				w.octet(0)
			}
			e.typ(w, f.Type, depth+1)
		}
	case reflect.Interface:
		// Method signatures use qualified type descriptions without recursive expansion.
		// Nonempty anonymous interfaces are rejected rather than ambiguously identified.
		if t.NumMethod() != 0 {
			e.fail(ErrUnsupported, t)
		}
	}
}

func (e *encoder) special(w *buffer, v reflect.Value) bool {
	if !v.CanInterface() {
		e.fail(ErrUnsupported, v.Type())
		return true
	}
	switch x := v.Interface().(type) {
	case time.Time:
		// Unix seconds and nanoseconds preserve the instant beyond UnixNano's range.
		w.number(uint64(x.Unix()))
		w.number(uint64(x.Nanosecond()))
	case decimal.Decimal:
		coefficient := x.Coefficient()
		if coefficient.BitLen() > maxWorkBytes {
			e.fail(ErrLimit, v.Type())
			return true
		}
		e.decimal(w, coefficient.String(), int64(x.Exponent()))
	case big.Int:
		if x.BitLen() > maxWorkBytes {
			e.fail(ErrLimit, v.Type())
			return true
		}
		w.text(x.String())
	case big.Rat:
		if x.Num().BitLen() > maxWorkBytes || x.Denom().BitLen() > maxWorkBytes {
			e.fail(ErrLimit, v.Type())
			return true
		}
		w.text(x.Num().String())
		w.text(x.Denom().String())
	case big.Float:
		if x.Prec() > maxWorkBytes {
			e.fail(ErrLimit, v.Type())
			return true
		}
		if x.Sign() == 0 {
			w.text("0")
		} else {
			w.text(x.Text('p', 0))
		}
	case json.Number:
		s := string(x)
		if len(s) > maxWorkBytes-e.work {
			e.fail(ErrLimit, v.Type())
			return true
		}
		if s == "" || !(s[0] == '-' || s[0] >= '0' && s[0] <= '9') || strings.TrimSpace(s) != s || !json.Valid([]byte(s)) {
			e.fail(ErrInvalid, v.Type())
			return true
		}
		d, err := decimal.NewFromString(s)
		if err != nil {
			e.fail(ErrInvalid, v.Type())
			return true
		}
		e.decimal(w, d.Coefficient().String(), int64(d.Exponent()))
	case net.IP:
		if x == nil {
			w.octet(0)
			return true
		}
		w.octet(1)
		ip := x.To16()
		if ip == nil {
			e.fail(ErrInvalid, v.Type())
			return true
		}
		w.blob(ip)
	case netip.Addr:
		if !x.IsValid() {
			w.octet(0)
			return true
		}
		w.octet(1)
		ip := x.Unmap()
		w.blob(ip.AsSlice())
		w.text(ip.Zone())
	default:
		return false
	}
	return true
}

func (e *encoder) decimal(w *buffer, coefficient string, exponent int64) {
	if coefficient == "0" {
		w.text("0")
		w.number(0)
		return
	}
	trimmed := strings.TrimRight(coefficient, "0")
	exponent += int64(len(coefficient) - len(trimmed))
	w.text(trimmed)
	w.number(uint64(exponent))
}
