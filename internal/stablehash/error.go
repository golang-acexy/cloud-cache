package stablehash

import "errors"

var (
	// ErrUnsupported indicates a value whose canonical semantics are not supported.
	ErrUnsupported = errors.New("unsupported stable hash value")
	// ErrCycle indicates a reference cycle on the current traversal path.
	ErrCycle = errors.New("cyclic stable hash value")
	// ErrLimit indicates that a depth, node, or encoding-work limit was exceeded.
	ErrLimit = errors.New("stable hash resource limit exceeded")
	// ErrInvalid indicates an invalid value of an otherwise supported type.
	ErrInvalid = errors.New("invalid stable hash value")
)
