package textsimilarity

import (
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

// Opt-in verification of every root on the same external corpus as the benchmark.
// The reference uses linear exact-line search. Compare before
// deduplication, whose first-arriving level was already scheduling dependent.
func TestComparisonCorpusEquivalence(t *testing.T) {
	names, texts := readComparisonCorpus(t)
	opts := &Options{MinSimilarLines: 10}
	files := loadComparisonCorpus(t, names, texts, opts)

	search := newLineSearch(files, opts)

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

				indexed := &fileToCheck{f: files[idx], inputs: files, fileID: idx, search: search}
				linear := &fileToCheck{f: files[idx], inputs: files, fileID: idx}
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
