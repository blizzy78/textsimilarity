package textsimilarity

import (
	"strings"
	"sync"
	"testing"
)

func TestLineQueryHitLimitPreservesLookup(t *testing.T) {
	opts := &Options{MinSimilarLines: 4, MaxLineDistance: 3}
	text := strings.Repeat("same\n", maxLineQueryHits/2+1)

	files := []*File{newFile("first", text), newFile("second", text)}
	for _, file := range files {
		if err := file.load(opts); err != nil {
			t.Fatal(err)
		}
	}

	search := newLineSearch(files, opts)
	needle := files[0].lines[0]

	query := search.query(t.Context(), needle, opts)
	if !query.fallback || len(query.hits) != 0 || len(query.files) != 0 {
		t.Fatal("a broad query retained its unbounded match list")
	}

	indexed := &fileToCheck{f: files[1], search: search, fileID: 1}
	indexed.setDone(0, true)

	linear := &fileToCheck{f: files[1], linesDone: indexed.linesDone}
	for _, start := range []int{0, 1, 100, len(files[1].lines)} {
		got, gotLevel := lineIndex(t.Context(), indexed, needle, start, opts)

		want, wantLevel := lineIndex(t.Context(), linear, needle, start, opts)
		if got != want || gotLevel != wantLevel {
			t.Fatalf("start %d: bounded lookup %d/%d, linear %d/%d", start, got, gotLevel, want, wantLevel)
		}
	}
}

func TestLineQueryCacheBudgetConcurrent(t *testing.T) {
	opts := &Options{MinSimilarLines: 4, MaxEditDistance: 1, MaxLineDistance: 3}

	var lines []string
	for letter := 'A'; letter <= 'T'; letter++ {
		lines = append(lines, strings.Repeat(string(letter), 12))
	}

	file := newFile("input", strings.Join(lines, "\n"))
	if err := file.load(opts); err != nil {
		t.Fatal(err)
	}

	search := newLineSearch([]*File{file}, opts)
	// Leave room for one tiny query, then race independent query builders.
	search.queryBytes.Store(maxLineQueryBytes - 24)

	var workers sync.WaitGroup
	for _, line := range file.lines {
		workers.Go(func() {
			query := search.query(t.Context(), line, opts)
			if query.fallback && (len(query.hits) != 0 || len(query.files) != 0) {
				t.Error("an uncached query retained its discarded match lists")
			}
		})
	}

	workers.Wait()

	if search.queryBytes.Load() > maxLineQueryBytes {
		t.Fatal("concurrent builders exceeded the match-cache budget")
	}

	for idx, line := range file.lines {
		indexed := &fileToCheck{f: file, search: search}

		got, _ := lineIndex(t.Context(), indexed, line, 0, opts)
		if got != idx {
			t.Fatalf("cache exhaustion lost a match: got %d, want %d", got, idx)
		}
	}
}
