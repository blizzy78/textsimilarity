package textsimilarity

import (
	"context"
	"math/rand/v2"
	"strings"
	"testing"
)

func TestLineSearchMatchesLinearSearch(t *testing.T) { //nolint:gocognit,cyclop // randomized lookups include independent marker and cancellation states
	random := rand.New(rand.NewPCG(71, 72)) //nolint:gosec // deterministic test data
	pool := []string{"", "a", "alpha", "alphabet", "func main() {}", "const n = 1;", "# Heading", "Ordinary prose", "日本語の文字列", "é é 🚀", "\xff\xfe", strings.Repeat("x", 255), strings.Repeat("x", 256)}

	for trial := 0; trial < 100; trial++ {
		opts := &Options{MinSimilarLines: 4}

		files := make([]*File, 3)
		for idx := range files {
			lines := make([]string, 30)
			for line := range lines {
				lines[line] = pool[random.IntN(len(pool))]
			}

			files[idx] = newFile("input", strings.Join(lines, "\n")+"\n")
			if err := files[idx].loadBuffered(opts, make([]byte, 4096)); err != nil {
				t.Fatal(err)
			}
		}

		search := newLineSearch(files, opts)
		if search == nil {
			t.Fatal("eligible input did not build a line index")
		}

		for fileID, file := range files {
			indexed := &fileToCheck{f: file, search: search, fileID: fileID, linesDone: newBitVector(len(file.lines))}

			linear := &fileToCheck{f: file, linesDone: indexed.linesDone}
			for idx := range file.lines {
				if random.IntN(5) == 0 {
					indexed.setDone(idx, true)
				}
			}

			for range 40 {
				needle := files[random.IntN(len(files))].lines[random.IntN(30)]
				start := random.IntN(31)
				got, level := lineIndex(t.Context(), indexed, needle, start, opts)

				want, wantLevel := lineIndex(t.Context(), linear, needle, start, opts)
				if got != want || level != wantLevel {
					t.Fatalf("trial %d: got %d/%d, want %d/%d", trial, got, level, want, wantLevel)
				}
			}

			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			if got, _ := lineIndex(ctx, indexed, file.lines[0], 0, opts); got != -1 {
				t.Fatal("canceled lookup returned an occurrence")
			}
		}
	}
}

func TestLineSearchBroadExactMatch(t *testing.T) {
	opts := &Options{}

	file := newFile("repeated", strings.Repeat("exact\n", 5000))
	if err := file.load(opts); err != nil {
		t.Fatal(err)
	}

	search := newLineSearch([]*File{file}, opts)

	indexed := &fileToCheck{f: file, search: search}
	for line := range 4999 {
		indexed.setDone(line, true)
	}

	for range 100 {
		got, level := lineIndex(t.Context(), indexed, textToFileLine("exact", opts), 0, opts)
		if got != 4999 || level != EqualSimilarityLevel {
			t.Fatal("broad lookup lost matches beyond the old query-cache limit")
		}
	}
}

func TestIndexedAcceptedLinesRespectIgnoredMarkers(t *testing.T) {
	opts := &Options{MaxLineDistance: 3, Flags: IgnoreBlankLinesFlag}

	file := newFile("input", "seed\n\nlater accepted line")
	if err := file.load(opts); err != nil {
		t.Fatal(err)
	}

	search := newLineSearch([]*File{file}, opts)
	indexed := &fileToCheck{f: file, search: search}
	indexed.setDone(1, true)

	if positions := acceptedLinePositions(t.Context(), indexed, 1, opts); len(positions) != 0 {
		t.Fatal("shared accepted-line index crossed a marker on an ignored line")
	}
}
