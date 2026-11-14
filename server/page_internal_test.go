// SPDX-License-Identifier: MIT

package server

import (
	"math"
	"testing"
)

// TestPageBounds: the page of s and l is the items of the list from s on,
// at most l of them, for every value of the wire: 0, the length of the list
// and around it, and the ends of int32 and uint32, which pass the largest
// int of a 32-bit platform.
func TestPageBounds(t *testing.T) {
	edges := func(all int) []uint32 {
		vs := []uint32{0, 1, math.MaxInt32 - 1, math.MaxInt32, math.MaxInt32 + 1, math.MaxUint32 - 1, math.MaxUint32}
		for _, d := range []int{-1, 0, 1} {
			if all+d >= 0 {
				vs = append(vs, uint32(all+d))
			}
		}
		return vs
	}
	for _, all := range []int{0, 1, 3, 255} {
		for _, start := range edges(all) {
			for _, limit := range edges(all) {
				lo, hi := pageBounds(start, limit, all)
				wantLo := min(uint64(start), uint64(all))
				wantHi := min(uint64(start)+uint64(limit), uint64(all))
				wantHi = max(wantHi, wantLo)
				if uint64(lo) != wantLo || uint64(hi) != wantHi || lo < 0 || hi < lo || hi > all {
					t.Errorf("pageBounds(%d, %d, %d) = %d, %d; want %d, %d", start, limit, all, lo, hi, wantLo, wantHi)
				}
			}
		}
	}
	// A negative length is no list.
	if lo, hi := pageBounds(1, 1, -1); lo != 0 || hi != 0 {
		t.Errorf("pageBounds(1, 1, -1) = %d, %d", lo, hi)
	}
	// The bounds slice the items.
	items := make([]int, 5)
	for _, c := range [][2]uint32{{0, 5}, {4, 1}, {5, 1}, {math.MaxInt32 + 1, 2}, {2, math.MaxUint32}} {
		lo, hi := pageBounds(c[0], c[1], len(items))
		// Compared in 64 bits: the limit does not fit the int of a 32-bit
		// platform.
		if page := items[lo:hi]; uint64(len(page)) > uint64(c[1]) {
			t.Errorf("s=%d l=%d: %d items", c[0], c[1], len(page))
		}
	}
}
