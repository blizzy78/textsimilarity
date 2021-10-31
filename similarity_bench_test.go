package textsimilarity

import (
	"context"
	"testing"
)

var Line int
var Level SimilarityLevel

func BenchmarkLineIndex(b *testing.B) {
	file := newFileToCheck(b,
		[]string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "dddddddddd", "eeeeeeeeee"},
		[]bool{false, false, false, false, false},
	)

	needle := newFileLine("aaaaaaaaaa")

	opts := Options{MaxEditDistance: 2}

	for n := 0; n < b.N; n++ {
		Line, Level = lineIndex(context.Background(), file, needle, 0, &opts)
	}
}
