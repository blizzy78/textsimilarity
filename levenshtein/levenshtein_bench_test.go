package levenshtein

import "testing"

var Dist int

func BenchmarkDistance64(b *testing.B) {
	b.StopTimer()

	s1 := []rune("Cras enim velit")
	s2 := []rune("Cras enim velit")

	b.StartTimer()

	for b.Loop() {
		Dist = Distance(s1, s2)
	}
}

func BenchmarkDistance(b *testing.B) {
	b.StopTimer()

	s1 := []rune("Cras enim velit, vehicula nec viverra at, elementum non augue. Praesent pulvinar mi volutpat enim blandit, vitae porta urna aliquam.")
	s2 := []rune("Cras enim velit, vehicula nec viverra at, elementum non augue. Praesent pulvinar mi volutpat enim blandit, vitae porta urna aliquam.")

	b.StartTimer()

	for b.Loop() {
		Dist = Distance(s1, s2)
	}
}
