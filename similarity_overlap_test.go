package textsimilarity

import (
	"math/rand/v2"
	"regexp"
	"testing"
)

func TestFilterOverlapsReference(t *testing.T) { //nolint:gocognit,cyclop // generate overlapping ranges and compare all state with the reference
	random := rand.New(rand.NewPCG(11, 12)) //nolint:gosec // reproducible test data

	for range 500 {
		files := []*File{{}, {}, {}}
		actualFiles := make([]*fileToCheck, 6)
		referenceFiles := make([]*fileToCheck, 6)

		for idx := range actualFiles {
			actualFiles[idx] = &fileToCheck{f: files[idx%len(files)], linesDone: newBitVector(80)}

			referenceFiles[idx] = &fileToCheck{f: files[idx%len(files)], linesDone: newBitVector(80)}
			for line := range 80 {
				actualFiles[idx].linesDone.set(line, true)
				referenceFiles[idx].linesDone.set(line, true)
			}
		}

		actual := make([]*FileOccurrence, random.IntN(100))

		reference := make([]*FileOccurrence, len(actual))
		for idx := range actual {
			file := random.IntN(len(actualFiles))
			start := random.IntN(64)
			end := start + 1 + random.IntN(16)
			actual[idx] = &FileOccurrence{Start: start, End: end, fileToCheck: actualFiles[file]}
			reference[idx] = &FileOccurrence{Start: start, End: end, fileToCheck: referenceFiles[file]}
		}

		got := filterSameFileOverlappingOccurrences(actual)

		want := referenceFilterOverlaps(reference)
		if len(got) != len(want) {
			t.Fatalf("got %d occurrences, want %d", len(got), len(want))
		}

		for idx := range got {
			if got[idx].Start != want[idx].Start || got[idx].End != want[idx].End || got[idx].fileToCheck.f != want[idx].fileToCheck.f {
				t.Fatalf("occurrence %d differs", idx)
			}
		}

		for idx, file := range actualFiles {
			for line := range 80 {
				if file.linesDone.isSet(line) != referenceFiles[idx].linesDone.isSet(line) {
					t.Fatalf("file %d line %d has different done state", idx, line)
				}
			}
		}
	}
}

func TestMinimumProbeNecessary(t *testing.T) { //nolint:gocognit,cyclop // compare randomized probes with full expansion
	random := rand.New(rand.NewPCG(13, 14)) //nolint:gosec // reproducible test data
	texts := []string{"", " ", "alpha", "alpha!", "beta", "ignore", "🚀", "𨊂", "gamma"}

	for range 2000 {
		opts := &Options{MinSimilarLines: 2 + random.IntN(8), MaxLineDistance: random.IntN(4)}
		if random.IntN(2) == 0 {
			opts.Flags |= IgnoreBlankLinesFlag
		}

		if random.IntN(2) == 0 {
			opts.IgnoreLineRegex = regexp.MustCompile("^ignore$")
		}

		left := &fileToCheck{f: &File{}, linesDone: newBitVector(16)}
		right := &fileToCheck{f: &File{}, linesDone: newBitVector(16)}

		var leftDone, rightDone [16]bool

		for line := range 16 {
			text := texts[random.IntN(len(texts))]

			other := text
			if random.IntN(4) == 0 {
				other = texts[random.IntN(len(texts))]
			}

			if line == 0 {
				text, other = "seed", "seed"
			}

			left.f.lines = append(left.f.lines, textToFileLine(text, opts))
			right.f.lines = append(right.f.lines, textToFileLine(other, opts))

			if line > 0 && random.IntN(8) == 0 {
				left.linesDone.set(line, true)
				leftDone[line] = true
			}

			if line > 0 && random.IntN(8) == 0 {
				right.linesDone.set(line, true)
				rightDone[line] = true
			}
		}

		possible := canReachMinimum(t.Context(), left, 0, right, 0, opts)
		for line := range 16 {
			if left.linesDone.isSet(line) != leftDone[line] || right.linesDone.isSet(line) != rightDone[line] {
				t.Fatal("minimum probe changed line markers")
			}
		}

		occurrences := []*FileOccurrence{
			{Start: 0, End: 1, fileToCheck: left},
			{Start: 0, End: 1, fileToCheck: right},
		}
		expandOccurrences(t.Context(), occurrences, opts)

		if occurrenceLongEnough(t.Context(), occurrences[0], opts) && occurrenceLongEnough(t.Context(), occurrences[1], opts) && !possible {
			t.Fatalf("probe rejected an expansion reaching line %d with minimum %d", occurrences[0].End, opts.MinSimilarLines)
		}
	}
}

// Keep the previous greedy algorithm as an independent reference. Input order
// determines priority, including when several checkers refer to the same File.
func referenceFilterOverlaps(occurrences []*FileOccurrence) []*FileOccurrence {
	var accepted []*FileOccurrence

	for _, occurrence := range occurrences {
		overlap := false

		for _, previous := range accepted {
			if previous.fileToCheck.f == occurrence.fileToCheck.f && previous.Start < occurrence.End && occurrence.Start < previous.End {
				overlap = true
				break
			}
		}

		if overlap {
			for line := occurrence.Start; line < occurrence.End; line++ {
				occurrence.fileToCheck.linesDone.set(line, false)
			}
		} else {
			accepted = append(accepted, occurrence)
		}
	}

	return accepted
}
