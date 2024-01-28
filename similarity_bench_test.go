package textsimilarity

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
)

var Line int
var Level SimilarityLevel

func BenchmarkLineIndex(b *testing.B) {
	b.StopTimer()

	file := newFileToCheck(b,
		[]string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "dddddddddd", "eeeeeeeeee"},
		[]bool{false, false, false, false, false},
	)

	needle := newFileLine("aaaaaaaaaa")

	opts := Options{MaxEditDistance: 2}

	ctx := context.Background()

	b.StartTimer()

	for n := 0; n < b.N; n++ {
		Line, Level = lineIndex(ctx, file, needle, 0, &opts)
	}
}

func BenchmarkLineIndex_Large(b *testing.B) {
	b.StopTimer()

	osFile, _ := os.Open("testdata/lipsum.txt")
	defer osFile.Close() //nolint:errcheck // file is being read

	data, _ := io.ReadAll(osFile)
	texts := strings.Split(string(data), "\n")

	file := newFileToCheck(b, texts, make([]bool, len(texts)))

	needle := newFileLine(texts[50][:10] + "x" + texts[50][10:])

	opts := Options{MaxEditDistance: 2}

	ctx := context.Background()

	b.StartTimer()

	for n := 0; n < b.N; n++ {
		Line, Level = lineIndex(ctx, file, needle, 0, &opts)
	}
}
