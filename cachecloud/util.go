package cachecloud

import (
	"github.com/acexy/golang-toolkit/crypto/hashing"
	"github.com/acexy/golang-toolkit/math/random"
	"sync"
)

var nodeId string
var nodeOnce sync.Once

func getNodeId() string {
	nodeOnce.Do(func() {
		nodeId = hashing.Md5Hex(random.UUID())
	})
	return nodeId
}
