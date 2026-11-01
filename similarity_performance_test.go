package textsimilarity

import (
	"context"
	"os"
	"strings"
	"testing"
)

func BenchmarkLinesSimilarity_Source(b *testing.B) {
	text := "result := service.LookupUser(ctx, request.UserID, options)"

	left := textToFileLine(text, &Options{})
	for _, test := range []struct{ name, text string }{
		{"equal", text},
		{"changed", text + ";"},
	} {
		b.Run(test.name, func(b *testing.B) {
			right := textToFileLine(test.text, &Options{})
			for b.Loop() {
				Level = linesSimilarity(left, right)
			}
		})
	}
}

var similarityCount int

func TestExpandOccurrencesMarksCommittedLines(t *testing.T) {
	left := newFileToCheck(t, []string{"seed1", "seed2", "", "next", "different"}, make([]bool, 5), false)
	right := newFileToCheck(t, []string{"seed1", "seed2", "", "next", "xxxxxxxxx"}, make([]bool, 5), false)
	occurrences := []*FileOccurrence{
		{Start: 0, End: 2, fileToCheck: left},
		{Start: 0, End: 2, fileToCheck: right},
	}
	opts := &Options{Flags: IgnoreBlankLinesFlag}

	level := expandOccurrences(context.Background(), occurrences, EqualSimilarityLevel, opts)
	if level != EqualSimilarityLevel {
		t.Fatalf("level = %d, want equal", level)
	}

	for _, occurrence := range occurrences {
		if occurrence.End != 4 {
			t.Fatalf("end = %d, want 4", occurrence.End)
		}

		for idx := range occurrence.fileToCheck.f.lines {
			if done := occurrence.fileToCheck.linesDone.isSet(idx); done != (idx < 4) {
				t.Fatalf("line %d done = %t, want %t", idx, done, idx < 4)
			}
		}
	}
}

// BenchmarkSimilarities includes loading, normalization, comparisons, occurrence
// expansion, and both output channels, using the repository's existing fixture.
func BenchmarkSimilarities(b *testing.B) {
	data, err := os.ReadFile("testdata/lipsum.txt")
	if err != nil {
		b.Fatal(err)
	}

	text := string(data)
	modified := strings.ReplaceAll(text, "Lorem", "Lxrem")
	opts := &Options{MinSimilarLines: 3, MaxLineDistance: 1}

	b.ResetTimer()

	for b.Loop() {
		files := []*File{
			{Name: "original.txt", R: strings.NewReader(text)},
			{Name: "modified.txt", R: strings.NewReader(modified)},
		}

		sims, progress, err := Similarities(context.Background(), files, opts)
		if err != nil {
			b.Fatal(err)
		}

		count := countBenchmarkSimilarities(sims, progress)
		if count == 0 {
			b.Fatal("no similarities found")
		}

		similarityCount = count
	}
}

func countBenchmarkSimilarities(sims <-chan *Similarity, progress <-chan Progress) int {
	done := make(chan struct{})

	go func() {
		for range progress {
		}

		close(done)
	}()

	count := 0
	for range sims {
		count++
	}

	<-done

	return count
}
