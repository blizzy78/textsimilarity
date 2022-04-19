package levenshtein

import "testing"

var Dist int

func BenchmarkDistance64(b *testing.B) {
	s1 := []rune("Cras enim velit")
	s2 := []rune("Cras enim velit")

	for i := 0; i < b.N; i++ {
		Dist = Distance(s1, s2)
	}
}

func BenchmarkDistance(b *testing.B) {
	s1 := []rune("Cras enim velit, vehicula nec viverra at, elementum non augue. Praesent pulvinar mi volutpat enim blandit, vitae porta urna aliquam.")
	s2 := []rune("Cras enim velit, vehicula nec viverra at, elementum non augue. Praesent pulvinar mi volutpat enim blandit, vitae porta urna aliquam.")

	for i := 0; i < b.N; i++ {
		Dist = Distance(s1, s2)
	}
}
