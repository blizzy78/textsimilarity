//go:build amd64 && goexperiment.simd

package textsimilarity

import "simd/archsimd"

func blockLengthsMatch(left []int32, right []int32, limit int32) bool {
	if !archsimd.X86.AVX2() {
		return scalarLengthsMatch(left, right, limit)
	}

	threshold := archsimd.BroadcastInt32x8(limit)

	for len(left) >= 8 {
		x := archsimd.LoadInt32x8Array((*[8]int32)(left))

		y := archsimd.LoadInt32x8Array((*[8]int32)(right))
		if x.Sub(y).Abs().LessEqual(threshold).ToBits() != 255 {
			return false
		}

		left, right = left[8:], right[8:]
	}

	return scalarLengthsMatch(left, right, limit)
}
