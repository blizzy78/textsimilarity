package textsimilarity

import (
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

// Opt-in verification of every root on the same external corpus as the benchmark.
// The reference uses C's block filter and linear line search. Compare before
// deduplication, whose first-arriving level was already scheduling dependent.
func TestComparisonCorpusEquivalence(t *testing.T) {
	names, texts := readComparisonCorpus(t)
	opts := &Options{MinSimilarLines: 10}
	files := loadComparisonCorpus(t, names, texts, opts)
	prepareComparisonCorpus(files, opts)

	search, blocks := newLineSearch(files, opts), newAnchorSearch(files, opts)
	if search == nil || blocks == nil {
		t.Fatal("corpus exceeds indexed comparison bounds")
	}

	search.blocks = blocks
	reference := newBlockSearch(files, opts)

	var (
		next    atomic.Uint32
		workers sync.WaitGroup
	)
	for range runtime.GOMAXPROCS(0) {
		workers.Go(func() {
			for {
				idx := int(next.Add(1)) - 1
				if idx >= len(files) {
					return
				}

				indexed := &fileToCheck{f: files[idx], inputs: files, fileID: idx, search: search, blocks: blocks}
				linear := &fileToCheck{f: files[idx], inputs: files, fileID: idx, blocks: reference}
				got := scanRecords(fileSimilarities(t.Context(), indexed, opts))

				want := scanRecords(fileSimilarities(t.Context(), linear, opts))
				if !reflect.DeepEqual(got, want) {
					t.Errorf("root %s: indexed %v, reference %v", names[idx], got, want)
				}
			}
		})
	}

	workers.Wait()
}
