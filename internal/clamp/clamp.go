// SPDX-License-Identifier: MIT

// Package clamp converts integers to the narrower types of the wire, at the
// bounds of the target type instead of wrapping around: a count of more
// than 2^32 - 1 items is sent as 2^32 - 1, not as its remainder.
package clamp

import "math"

// Uint32 returns n within [0, math.MaxUint32].
func Uint32(n int) uint32 { return Uint32FromInt64(int64(n)) }

// Uint32FromInt64 returns v within [0, math.MaxUint32].
func Uint32FromInt64(v int64) uint32 {
	if v < 0 {
		return 0
	}
	if v > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(v)
}

// Uint32FromUint64 returns v within [0, math.MaxUint32].
func Uint32FromUint64(v uint64) uint32 {
	if v > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(v)
}

// Uint16 returns n within [0, math.MaxUint16].
func Uint16(n int) uint16 {
	v := int64(n)
	if v < 0 {
		return 0
	}
	if v > math.MaxUint16 {
		return math.MaxUint16
	}
	return uint16(v)
}
