package cachecloud

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func syncMessage(t *testing.T, event cacheSyncEvent) *redis.Message {
	t.Helper()
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal sync event: %v", err)
	}
	return &redis.Message{Payload: string(payload)}
}

func TestHandleSyncMessageDispatchesValidEvent(t *testing.T) {
	want := cacheSyncEvent{NodeID: "another-node", BucketName: "bucket", CacheKey: "model:1", Operation: cacheSyncPut, ValueHash: "hash", ExpireAt: time.Now().Add(time.Minute).UnixMilli()}
	var got cacheSyncEvent
	handleSyncMessage(syncMessage(t, want), "test", func(event cacheSyncEvent) { got = event })
	if got != want {
		t.Fatalf("unexpected dispatched event: got %+v, want %+v", got, want)
	}
}

func TestHandleSyncMessageIgnoresSameNode(t *testing.T) {
	called := false
	handleSyncMessage(syncMessage(t, cacheSyncEvent{NodeID: getNodeID(), BucketName: "bucket", CacheKey: "model:1", Operation: cacheSyncDelete}), "test", func(cacheSyncEvent) { called = true })
	if called {
		t.Fatal("same-node event should be ignored")
	}
}

func TestHandleSyncMessageRejectsInvalidInput(t *testing.T) {
	invalidEvents := []cacheSyncEvent{
		{NodeID: "another-node"},
		{NodeID: "another-node", BucketName: "bucket", CacheKey: "key"},
		{NodeID: "another-node", BucketName: "bucket", CacheKey: "key", Operation: cacheSyncPut},
		{NodeID: "another-node", BucketName: "bucket", CacheKey: "key", Operation: "unknown"},
	}
	messages := []*redis.Message{nil, {Payload: "not-json"}}
	for _, event := range invalidEvents {
		messages = append(messages, syncMessage(t, event))
	}
	for _, message := range messages {
		called := false
		handleSyncMessage(message, "test", func(cacheSyncEvent) { called = true })
		if called {
			t.Fatalf("invalid message should not be dispatched: %+v", message)
		}
	}
}

func TestSyncEventSupportsDelimiterInCacheKey(t *testing.T) {
	expireAt := time.Now().Add(time.Minute).UnixMilli()
	payload, err := newSyncEventPayload("bucket", "key<@.>part", cacheSyncPut, "hash", expireAt)
	if err != nil {
		t.Fatalf("create sync payload: %v", err)
	}
	var event cacheSyncEvent
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		t.Fatalf("decode sync payload: %v", err)
	}
	if event.CacheKey != "key<@.>part" || event.ValueHash != "hash" || event.ExpireAt != expireAt {
		t.Fatalf("sync event changed during encoding: %+v", event)
	}
}
