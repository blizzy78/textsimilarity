package textsimilarity

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func testAlignmentLines(texts []string, opts *Options) alignmentLines {
	file := &File{}

	positions := make([]int, len(texts))
	for index, text := range texts {
		positions[index] = index

		file.lines = append(file.lines, textToFileLine(text, opts))
	}

	return alignmentLines{file: file, positions: positions}
}

func TestSharedAnchorIndexMatchesFreshSubranges(t *testing.T) {
	random := rand.New(rand.NewPCG(221, 222)) //nolint:gosec // reproducible ranges with duplicates outside each view
	opts := &Options{MaxLineDistance: 3, AlwaysDifferentLineRegex: regexp.MustCompile("^forced")}
	pool := []string{"alpha", "beta", "gamma", "repeated", "forced"}

	for trial := range 500 {
		var lines [2]alignmentLines
		for side := range lines {
			texts := make([]string, 0, 20)
			for range 20 {
				texts = append(texts, pool[random.IntN(len(pool))])
			}

			lines[side] = testAlignmentLines(texts, opts)
		}

		search := newLineSearch([]*File{lines[0].file, lines[1].file}, opts)
		for side := range lines {
			start := random.IntN(21)
			end := start + random.IntN(21-start)
			lines[side].positions = lines[side].positions[start:end]
		}

		want := matchingLineBlocks(t.Context(), lines[0], lines[1])
		lines[0].index, lines[1].index = search.files[0], search.files[1]

		got := matchingLineBlocks(t.Context(), lines[0], lines[1])
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d: shared index %v, fresh subrange index %v", trial, got, want)
		}
	}
}

func TestAmbiguousLinesAreNotSynchronizationAnchors(t *testing.T) {
	opts := &Options{}
	source := testAlignmentLines([]string{"source start", "repeated", "source middle", "repeated", "source end"}, opts)

	peer := testAlignmentLines([]string{"peer start", "repeated", "peer middle", "repeated", "peer end"}, opts)
	if blocks := matchingLineBlocks(t.Context(), source, peer); len(blocks) != 0 {
		t.Fatalf("ambiguous lines synchronized unrelated content: %v", blocks)
	}
}

func TestUniqueAnchorsAgainstQuadraticSubsequence(t *testing.T) { //nolint:gocognit,cyclop // compare unique anchor chains with a slow independent reference
	random := rand.New(rand.NewPCG(201, 202)) //nolint:gosec // deterministic test corpus
	opts := &Options{AlwaysDifferentLineRegex: regexp.MustCompile("^forced")}
	pool := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "forced"}

	for trial := range 1000 {
		texts := make([][]string, 2)
		for index := range texts {
			for range random.IntN(20) {
				texts[index] = append(texts[index], pool[random.IntN(len(pool))])
			}
		}

		source, peer := testAlignmentLines(texts[0], opts), testAlignmentLines(texts[1], opts)

		var pairs []lineMatch

		for left, text := range texts[0] {
			if text == "forced" {
				continue
			}

			leftCount, rightCount, rightPosition := 0, 0, 0

			for _, candidate := range texts[0] {
				if candidate == text {
					leftCount++
				}
			}

			for right, candidate := range texts[1] {
				if candidate == text {
					rightCount++
					rightPosition = right
				}
			}

			if leftCount == 1 && rightCount == 1 {
				pairs = append(pairs, lineMatch{source: left, peer: rightPosition, count: 1})
			}
		}

		lengths := make([]int, len(pairs))
		want := 0

		for index, pair := range pairs {
			lengths[index] = 1
			for previous := range index {
				if pairs[previous].peer < pair.peer {
					lengths[index] = max(lengths[index], lengths[previous]+1)
				}
			}

			want = max(want, lengths[index])
		}

		anchors := uniqueLineAnchors(t.Context(), source, peer, lineMatchTask{sourceEnd: len(texts[0]), peerEnd: len(texts[1])})
		if len(anchors) != want {
			t.Fatalf("trial %d: got %d anchors, want %d", trial, len(anchors), want)
		}

		for index, anchor := range anchors {
			if source.at(anchor.source).text != peer.at(anchor.peer).text || source.at(anchor.source).flagSet(matchesAlwaysDifferentLineFlag) {
				t.Fatal("anchor does not identify an exact eligible match")
			}

			if index > 0 && (anchors[index-1].source >= anchor.source || anchors[index-1].peer >= anchor.peer) {
				t.Fatal("anchor chain crosses line order")
			}
		}
	}
}

func TestRepeatedTextStillSupportsExactCopiesAndSingleInsertions(t *testing.T) {
	opts := &Options{MaxLineDistance: 1}
	source := strings.Split(strings.TrimSuffix("seed\n"+strings.Repeat("repeated paragraph\n", 2000), "\n"), "\n")

	for _, inserted := range []bool{false, true} {
		t.Run(fmt.Sprint(inserted), func(t *testing.T) {
			peer := append([]string(nil), source...)
			if inserted {
				peer = append(append([]string(nil), source[:500]...), "one inserted paragraph")
				peer = append(peer, source[500:]...)
			}

			occs := []*FileOccurrence{lineEditOccurrence(t, "source", source, opts), lineEditOccurrence(t, "peer", peer, opts)}
			expandOccurrences(t.Context(), occs, EqualSimilarityLevel, opts)

			if occs[0].End != len(source) || occs[1].End != len(peer) {
				t.Fatal("repeated text lost a well-defined prefix/suffix alignment")
			}
		})
	}
}

func TestMatchingBlocksStayOrderedAndExact(t *testing.T) {
	random := rand.New(rand.NewPCG(211, 212)) //nolint:gosec // deterministic test corpus
	opts := &Options{AlwaysDifferentLineRegex: regexp.MustCompile("^forced")}
	pool := []string{"alpha", "beta", "gamma", "repeated", "forced", "日本語", "\xff\xfe"}

	for trial := range 500 {
		var texts [2][]string
		for side := range texts {
			for range random.IntN(30) {
				texts[side] = append(texts[side], pool[random.IntN(len(pool))])
			}
		}

		source, peer := testAlignmentLines(texts[0], opts), testAlignmentLines(texts[1], opts)
		assertOrderedExactBlocks(t, trial, source, peer)
	}
}

func assertOrderedExactBlocks(t *testing.T, trial int, source alignmentLines, peer alignmentLines) {
	t.Helper()

	sourceEnd, peerEnd := 0, 0
	for _, block := range matchingLineBlocks(t.Context(), source, peer) {
		if block.count <= 0 || block.source < sourceEnd || block.peer < peerEnd {
			t.Fatalf("trial %d: overlapping or empty matching block", trial)
		}

		for offset := range block.count {
			left, right := source.at(block.source+offset), peer.at(block.peer+offset)
			if left.text != right.text || (left.flags|right.flags)&matchesAlwaysDifferentLineFlag != 0 {
				t.Fatalf("trial %d: a non-exact or forced-different line became a reset", trial)
			}
		}

		sourceEnd, peerEnd = block.source+block.count, block.peer+block.count
	}
}
