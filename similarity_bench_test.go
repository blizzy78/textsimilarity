package textsimilarity

import (
	"context"
	"testing"
)

var Line int
var Level SimilarityLevel

func BenchmarkLineIndex(b *testing.B) {
	b.StopTimer()

	file := newFileToCheck(b,
		[]string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "dddddddddd", "eeeeeeeeee"},
		[]bool{false, false, false, false, false},
		false,
	)

	needle := newFileLine("aaaaaaaaaa", false)

	opts := Options{}

	ctx := context.Background()

	b.StartTimer()

	for b.Loop() {
		Line, Level = lineIndex(ctx, file, needle, 0, &opts)
	}
}

func BenchmarkLineIndex_Large(b *testing.B) {
	b.StopTimer()

	file, needle := newLargeLineIndexFixture(b)

	opts := Options{}

	ctx := context.Background()

	b.StartTimer()

	for b.Loop() {
		Line, Level = lineIndex(ctx, file, needle, 0, &opts)
	}
}
