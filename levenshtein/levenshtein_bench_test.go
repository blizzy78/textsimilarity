package levenshtein

import "testing"

var Dist int

func BenchmarkDistance64(b *testing.B) {
	b.StopTimer()

	str1 := []rune("Cras enim velit")
	str2 := []rune("Cras enim velit")

	b.StartTimer()

	for b.Loop() {
		Dist = Distance(str1, str2)
	}
}

func BenchmarkDistance(b *testing.B) {
	b.StopTimer()

	str1 := []rune("Cras enim velit, vehicula nec viverra at, elementum non augue. Praesent pulvinar mi volutpat enim blandit, vitae porta urna aliquam.")
	str2 := []rune("Cras enim velit, vehicula nec viverra at, elementum non augue. Praesent pulvinar mi volutpat enim blandit, vitae porta urna aliquam.")

	b.StartTimer()

	for b.Loop() {
		Dist = Distance(str1, str2)
	}
}
