package cachecloud

import (
	"sync"

	"github.com/acexy/golang-toolkit/crypto/hashing"
	"github.com/acexy/golang-toolkit/math/conversion"
	"github.com/acexy/golang-toolkit/math/random"
	"github.com/acexy/golang-toolkit/util/date"
)

var nodeID string
var nodeOnce sync.Once

func getNodeID() string {
	nodeOnce.Do(func() {
		nodeID = hashing.Md5Hex(random.UUID() + conversion.FromInt64(date.CurrentUnixMilli()))
	})
	return nodeID
}
