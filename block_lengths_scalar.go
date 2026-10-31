//go:build !amd64 || !goexperiment.simd

package textsimilarity

func blockLengthsMatch(left []int32, right []int32, limit int32) bool {
	return scalarLengthsMatch(left, right, limit)
}
