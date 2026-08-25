package cachecloud

import (
	"testing"
	"time"
)

const cacheValueHashTestIterations = 256

type cacheValueHashTestModel struct {
	Name       string
	Attributes map[string]int
	Groups     map[string]map[int]string
	Tags       []string
}

func TestCacheValueHashStableForMapInsertionOrder(t *testing.T) {
	want := cacheValueHash(map[string]int{
		"alpha": 1,
		"beta":  2,
		"gamma": 3,
		"delta": 4,
	}, nil)

	keys := []string{"alpha", "beta", "gamma", "delta"}
	values := map[string]int{"alpha": 1, "beta": 2, "gamma": 3, "delta": 4}
	for iteration := range cacheValueHashTestIterations {
		value := make(map[string]int, len(keys))
		for offset := range len(keys) {
			key := keys[(iteration+offset)%len(keys)]
			value[key] = values[key]
		}
		if got := cacheValueHash(value, nil); got != want {
			t.Fatalf("map hash changed at iteration %d: got %s, want %s", iteration, got, want)
		}
	}
}

func TestCacheValueHashStableForNestedComplexValue(t *testing.T) {
	first := cacheValueHashTestModel{
		Name: "complex",
		Attributes: map[string]int{
			"alpha": 1,
			"beta":  2,
			"gamma": 3,
		},
		Groups: map[string]map[int]string{
			"first":  {2: "two", 1: "one"},
			"second": {4: "four", 3: "three"},
		},
		Tags: []string{"one", "two", "three"},
	}
	second := cacheValueHashTestModel{
		Name: "complex",
		Attributes: map[string]int{
			"gamma": 3,
			"alpha": 1,
			"beta":  2,
		},
		Groups: map[string]map[int]string{
			"second": {3: "three", 4: "four"},
			"first":  {1: "one", 2: "two"},
		},
		Tags: []string{"one", "two", "three"},
	}

	firstEnvelope, err := newCacheValueEnvelope(first, time.Minute)
	if err != nil {
		t.Fatalf("create first complex envelope: %v", err)
	}
	secondEnvelope, err := newCacheValueEnvelope(second, time.Minute)
	if err != nil {
		t.Fatalf("create second complex envelope: %v", err)
	}
	if firstEnvelope.ValueHash != secondEnvelope.ValueHash {
		t.Fatalf("logically equal complex values must have the same hash: first=%s second=%s", firstEnvelope.ValueHash, secondEnvelope.ValueHash)
	}
}

func TestCacheValueHashTreatsPointerAndValueEqually(t *testing.T) {
	value := cacheValueHashTestModel{
		Name:       "model",
		Attributes: map[string]int{"alpha": 1},
		Groups:     map[string]map[int]string{"group": {1: "one"}},
		Tags:       []string{"one"},
	}
	valueHash := cacheValueHash(value, nil)
	pointerHash := cacheValueHash(&value, nil)
	if valueHash != pointerHash {
		t.Fatalf("pointer and value hashes must match: value=%s pointer=%s", valueHash, pointerHash)
	}
}

func TestCacheValueHashChangesWithContent(t *testing.T) {
	base := cacheValueHash(map[string]int{"alpha": 1, "beta": 2}, nil)
	tests := []struct {
		name  string
		value any
	}{
		{name: "changed value", value: map[string]int{"alpha": 1, "beta": 3}},
		{name: "added field", value: map[string]int{"alpha": 1, "beta": 2, "gamma": 3}},
		{name: "removed field", value: map[string]int{"alpha": 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := cacheValueHash(test.value, nil); got == base {
				t.Fatalf("different content unexpectedly produced the same hash: %s", got)
			}
		})
	}
}

func TestCacheValueHashStableAcrossRepeatedEnvelopeEncoding(t *testing.T) {
	wantValue := cacheValueHashTestModel{
		Name:       "repeated",
		Attributes: map[string]int{"alpha": 1, "beta": 2, "gamma": 3},
		Groups:     map[string]map[int]string{"group": {1: "one", 2: "two"}},
		Tags:       []string{"one", "two"},
	}
	var wantHash string
	for iteration := range cacheValueHashTestIterations {
		envelope, err := newCacheValueEnvelope(wantValue, time.Minute)
		if err != nil {
			t.Fatalf("encode envelope at iteration %d: %v", iteration, err)
		}
		if iteration == 0 {
			wantHash = envelope.ValueHash
			continue
		}
		if envelope.ValueHash != wantHash {
			t.Fatalf("envelope hash changed at iteration %d: got %s, want %s", iteration, envelope.ValueHash, wantHash)
		}
	}
	if wantHash == "" {
		t.Fatal("expected a non-empty hash")
	}
}
