// SPDX-License-Identifier: MIT

package clamp

import (
	"math"
	"testing"
)

func TestUint32(t *testing.T) {
	for _, c := range []struct {
		in   int
		want uint32
	}{{-1, 0}, {math.MinInt, 0}, {0, 0}, {7, 7}, {math.MaxUint16, math.MaxUint16}} {
		if got := Uint32(c.in); got != c.want {
			t.Errorf("Uint32(%d) = %d, want %d", c.in, got, c.want)
		}
	}
	if math.MaxInt > math.MaxUint32 {
		big := int(int64(math.MaxUint32) + 1)
		if got := Uint32(big); got != math.MaxUint32 {
			t.Errorf("Uint32(%d) = %d, want %d", big, got, uint32(math.MaxUint32))
		}
	}
}

func TestUint32FromUint64(t *testing.T) {
	for _, c := range []struct {
		in   uint64
		want uint32
	}{{0, 0}, {math.MaxUint32, math.MaxUint32}, {math.MaxUint32 + 1, math.MaxUint32}, {math.MaxUint64, math.MaxUint32}} {
		if got := Uint32FromUint64(c.in); got != c.want {
			t.Errorf("Uint32FromUint64(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestUint16(t *testing.T) {
	for _, c := range []struct {
		in   int
		want uint16
	}{{-5, 0}, {0, 0}, {80, 80}, {math.MaxUint16, math.MaxUint16}, {math.MaxUint16 + 1, math.MaxUint16}, {math.MaxInt, math.MaxUint16}} {
		if got := Uint16(c.in); got != c.want {
			t.Errorf("Uint16(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestUint32FromInt64(t *testing.T) {
	for _, c := range []struct {
		in   int64
		want uint32
	}{{math.MinInt64, 0}, {-1, 0}, {0, 0}, {math.MaxUint32, math.MaxUint32}, {math.MaxUint32 + 1, math.MaxUint32}, {math.MaxInt64, math.MaxUint32}} {
		if got := Uint32FromInt64(c.in); got != c.want {
			t.Errorf("Uint32FromInt64(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}
