package cachecloud

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"reflect"
)

// encodeCacheValue reserves an empty payload for nil, including caller-owned pointer chains.
// Non-nil values retain the existing Gob wire format.
func encodeCacheValue(data any) ([]byte, error) {
	value := reflect.ValueOf(data)
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			return nil, nil
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return nil, nil
	}
	if (value.Kind() == reflect.Slice || value.Kind() == reflect.Map) && value.IsNil() {
		return nil, nil
	}
	var payload bytes.Buffer
	if err := gob.NewEncoder(&payload).Encode(data); err != nil {
		return nil, err
	}
	return payload.Bytes(), nil
}

func decodeCacheValue(payload []byte, result any) error {
	if len(payload) != 0 {
		return gob.NewDecoder(bytes.NewReader(payload)).Decode(result)
	}
	if isNilResult(result) {
		return ErrResultRequired
	}
	value := reflect.ValueOf(result)
	if value.Kind() != reflect.Pointer {
		return fmt.Errorf("cachecloud: result must be a non-nil pointer, got %T", result)
	}
	// Clear reused destinations so a cached nil never exposes a previous result.
	value.Elem().SetZero()
	return nil
}
