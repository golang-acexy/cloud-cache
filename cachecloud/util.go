package cachecloud

import (
	"github.com/acexy/golang-toolkit/crypto/hashing"
	"github.com/acexy/golang-toolkit/math/random"
)

var nodeId string

func getNodeId() string {
	if nodeId == "" {
		nodeId = hashing.Md5Hex(random.UUID())
	}
	return nodeId
}
