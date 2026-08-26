package cachecloud

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/zeebo/xxh3"
)

const cacheExpirationTolerance = 3 * time.Second

// cacheValueEnvelope 保存缓存的真实序列化内容及其同步元数据。
// ValueHash 始终基于 Payload 计算，保证广播摘要与实际缓存内容一致。
type cacheValueEnvelope struct {
	Payload   []byte
	ValueHash string
	ExpireAt  int64
}

func newCacheValueEnvelope(data any, expiration time.Duration) (cacheValueEnvelope, error) {
	var payload bytes.Buffer
	if err := gob.NewEncoder(&payload).Encode(data); err != nil {
		return cacheValueEnvelope{}, err
	}
	dataBytes := payload.Bytes()
	return cacheValueEnvelope{
		Payload:   dataBytes,
		ValueHash: cacheValueHash(data, dataBytes),
		ExpireAt:  time.Now().Add(expiration).UnixMilli(),
	}, nil
}

// cacheValueHash 优先使用 JSON 的确定性编码计算内容摘要，避免 map 的 Gob 编码顺序
// 在不同节点间产生误判。JSON 不支持的值仍以实际 Gob Payload 作为降级摘要输入。
func cacheValueHash(data any, fallbackPayload []byte) string {
	hashPayload, err := json.Marshal(data)
	if err != nil {
		hashPayload = fallbackPayload
	}
	return xxh3Hex(hashPayload)
}

func xxh3Hex(data []byte) string {
	sum128 := xxh3.Hash128(data)
	var sum [16]byte
	binary.BigEndian.PutUint64(sum[:8], sum128.Hi)
	binary.BigEndian.PutUint64(sum[8:], sum128.Lo)
	return hex.EncodeToString(sum[:])
}

func (e cacheValueEnvelope) decode(result any) error {
	return gob.NewDecoder(bytes.NewReader(e.Payload)).Decode(result)
}

func (e cacheValueEnvelope) expired(now time.Time) bool {
	return e.ExpireAt <= now.UnixMilli()
}

// extendExpiration 在不超过本地缓存有效期上限的前提下延长逻辑过期时间。
// 小于容差的时间差不会触发底层缓存重写，避免多节点间频繁续期。
func (e *cacheValueEnvelope) extendExpiration(remoteExpireAt int64, maxExpiration time.Duration, now time.Time) bool {
	targetExpireAt := minExpireAt(remoteExpireAt, now.Add(maxExpiration).UnixMilli())
	if targetExpireAt <= e.ExpireAt+cacheExpirationTolerance.Milliseconds() {
		return false
	}
	e.ExpireAt = targetExpireAt
	return true
}

func minExpireAt(first, second int64) int64 {
	if first < second {
		return first
	}
	return second
}
