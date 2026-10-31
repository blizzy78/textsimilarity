package textsimilarity

import (
	"math/rand/v2"
	"strings"
	"testing"
)

func TestBlockSearchNeverRejectsReportableGroup(t *testing.T) { //nolint:gocognit,cyclop // randomized differential check includes complete marker state
	random := rand.New(rand.NewPCG(21, 22)) //nolint:gosec // deterministic test inputs
	pool := []string{"", " ", "}", "func main() {}", "const value = 1;", "## Heading", "Ordinary prose", "日本語", "é é 🚀", "𨊂", strings.Repeat("a", 11), strings.Repeat("b", 16), strings.Repeat("c", 2821)}

	for trial := 0; trial < 500; trial++ {
		opts := &Options{MinSimilarLines: 4 + random.IntN(9), MaxEditDistance: 1 + random.IntN(6)}
		if trial%3 == 0 {
			opts.Flags = IgnoreWhitespaceFlag
		}

		shared := make([]string, 20)
		for idx := range shared {
			shared[idx] = pool[random.IntN(len(pool))]
		}

		files := make([]*File, 2+random.IntN(3))
		for idx := range files {
			lines := append([]string(nil), shared...)
			for range random.IntN(5) {
				lines[random.IntN(len(lines))] = pool[random.IntN(len(pool))]
			}

			files[idx] = &File{R: strings.NewReader(strings.Join(lines, "\n") + "\n")}
			if err := files[idx].loadBuffered(opts, blockWidth(opts), make([]byte, 4096)); err != nil {
				t.Fatal(err)
			}
		}

		index := newBlockSearch(files, opts)
		if index == nil {
			t.Fatal("eligible corpus did not build an in-memory filter")
		}

		fileID := random.IntN(len(files))
		start := random.IntN(len(files[fileID].lines))

		source := &fileToCheck{f: files[fileID], linesDone: newBitVector(len(files[fileID].lines)), peers: make([]*fileToCheck, len(files))}
		for idx, file := range files {
			peer := &fileToCheck{f: file, linesDone: newBitVector(len(file.lines))}
			for line := range file.lines {
				if random.IntN(12) == 0 {
					peer.linesDone.set(line, true)
				}
			}

			source.peers[idx] = peer
		}

		for line := range source.f.lines {
			if line != start && random.IntN(12) == 0 {
				source.linesDone.set(line, true)
			}
		}

		before := snapshotMarkers(source)
		possible := index.possible(t.Context(), source, start, opts)
		assertMarkers(t, source, before)
		seed := source.f.lines[start]
		occurrences := []*FileOccurrence{{Start: start, End: start + 1, fileToCheck: source}}
		level := EqualSimilarityLevel

		for _, peer := range source.peers {
			first := 0
			if peer.f == source.f {
				first = start + 1
			}

			found, foundLevel := lineOccurrences(t.Context(), peer, seed, first, opts)
			occurrences = append(occurrences, found...)
			level = min(level, foundLevel)
		}

		if len(occurrences) < 2 {
			continue
		}

		expandOccurrences(t.Context(), occurrences, level, opts)

		if occurrences[0].End-start < opts.MinSimilarLines {
			continue
		}

		if len(filterSameFileOverlappingOccurrences(occurrences)) >= 2 && !possible {
			t.Fatalf("trial %d: rejected a reportable group at source line %d", trial, start)
		}
	}
}

func snapshotMarkers(source *fileToCheck) [][]bool {
	files := append([]*fileToCheck{source}, source.peers...)

	result := make([][]bool, len(files))
	for idx, file := range files {
		result[idx] = make([]bool, len(file.f.lines))
		for line := range result[idx] {
			result[idx][line] = file.isDone(line)
		}
	}

	return result
}

func assertMarkers(t *testing.T, source *fileToCheck, want [][]bool) {
	t.Helper()

	files := append([]*fileToCheck{source}, source.peers...)
	for idx, file := range files {
		for line, value := range want[idx] {
			if file.isDone(line) != value {
				t.Fatalf("filter changed file %d line %d marker", idx, line)
			}
		}
	}
}

func TestLengthMatcherBoundaries(t *testing.T) {
	for size := 0; size <= 64; size++ {
		left, right := make([]int32, size), make([]int32, size)
		for idx := range left {
			left[idx] = int32(100 + idx)
			right[idx] = left[idx]
		}

		if !blockLengthsMatch(left, right, 5) {
			t.Fatalf("size %d: equal lengths rejected", size)
		}

		for idx := range right {
			right[idx] = left[idx] + 5
			if !blockLengthsMatch(left, right, 5) {
				t.Fatalf("size %d offset %d: boundary rejected", size, idx)
			}

			right[idx]++
			if blockLengthsMatch(left, right, 5) {
				t.Fatalf("size %d offset %d: over-limit accepted", size, idx)
			}

			right[idx] = left[idx]
		}
	}

	if blockLengthsMatch([]int32{0}, []int32{1<<31 - 1}, 5) {
		t.Fatal("large length difference accepted")
	}
}
