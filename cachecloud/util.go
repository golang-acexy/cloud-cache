package cachecloud

import (
	"sync"

	"github.com/acexy/golang-toolkit/crypto/hashing"
	"github.com/acexy/golang-toolkit/math/conversion"
	"github.com/acexy/golang-toolkit/math/random"
	"github.com/acexy/golang-toolkit/util/date"
)

var nodeId string
var nodeOnce sync.Once

func getNodeId() string {
	nodeOnce.Do(func() {
		nodeId = hashing.Md5Hex(random.UUID() + conversion.FromInt64(date.CurrentUnixMilli()))
	})
	return nodeId
}
