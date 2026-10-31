package textsimilarity

import (
	"context"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"testing"

	reference "github.com/agext/levenshtein"
)

// Check the complete line classification against an independent implementation,
// including mask collisions, Unicode, defaults, and always-different lines.
func TestLinesSimilarityReference(t *testing.T) { //nolint:gocognit,cyclop // compare random lines, flags, and thresholds with an independent implementation
	random := rand.New(rand.NewPCG(5, 6)) //nolint:gosec // deterministic test inputs, not secrets
	alphabet := []rune("aAbBcCé€𨊂🚀\ufffd")

	for range 2000 {
		left := make([]rune, random.IntN(80))
		for idx := range left {
			left[idx] = alphabet[random.IntN(len(alphabet))]
		}

		right := append([]rune(nil), left...)
		for range random.IntN(12) {
			if len(right) > 0 {
				right[random.IntN(len(right))] = alphabet[random.IntN(len(alphabet))]
			}
		}

		if random.IntN(2) == 0 {
			right = append(right, alphabet[random.IntN(len(alphabet))])
		}

		line1 := textToFileLine(string(left), &Options{})
		line2 := textToFileLine(string(right), &Options{})

		if random.IntN(10) == 0 {
			line1.flags |= matchesAlwaysDifferentLineFlag
		}

		if random.IntN(10) == 0 {
			line2.flags |= matchesAlwaysDifferentLineFlag
		}

		distance := reference.Distance(line1.text, line2.text, nil)

		for _, limit := range []int{-1, 0, 1, 2, 5, 10, 100} {
			effectiveLimit := limit
			if effectiveLimit <= 0 {
				effectiveLimit = DefaultMaxEditDistance
			}

			want := differentSimilarityLevel

			if !line1.flagSet(matchesAlwaysDifferentLineFlag) && !line2.flagSet(matchesAlwaysDifferentLineFlag) {
				if line1.text == line2.text {
					want = EqualSimilarityLevel
				} else if distance <= effectiveLimit {
					want = SimilarSimilarityLevel
				}
			}

			if got := linesSimilarity(line1, line2, &Options{MaxEditDistance: limit}); got != want {
				t.Fatalf("linesSimilarity(%q, %q, %d) = %d, want %d (distance %d)", line1.text, line2.text, limit, got, want, distance)
			}
		}
	}
}

// Changing both ends prevents trimming and exposes the cost of wider bands on
// ordinary source lines. Both lines still have an edit distance of two.
func BenchmarkLinesSimilarity_Source(b *testing.B) {
	text := "result := service.LookupUser(ctx, request.UserID, options)"
	left := textToFileLine(text, &Options{})
	right := textToFileLine("\t"+text+";", &Options{})

	for _, limit := range []int{0, 2, 5, 20, 50} {
		b.Run("limit="+strconv.Itoa(limit), func(b *testing.B) {
			opts := &Options{MaxEditDistance: limit}
			for b.Loop() {
				Level = linesSimilarity(left, right, opts)
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
	opts := &Options{Flags: IgnoreBlankLinesFlag, MaxEditDistance: 2}

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
	opts := &Options{MaxEditDistance: 2, MinSimilarLines: 3}

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
