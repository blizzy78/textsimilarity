package textsimilarity

import (
	"context"
	"sort"
)

type lineAlignment struct {
	source      alignmentLines
	peer        alignmentLines
	sourceIndex int
	peerIndex   int
	maximum     int
	budget      int
	run         int
	edited      bool
	end         int
	level       SimilarityLevel
}

func (alignment *lineAlignment) take(ctx context.Context, source bool, peer bool) bool {
	if contextDone(ctx) || alignment.sourceIndex >= alignment.maximum {
		return false
	}

	equal := source && peer && linesSimilarity(alignment.source.at(alignment.sourceIndex), alignment.peer.at(alignment.peerIndex)) == EqualSimilarityLevel
	if equal {
		alignment.run = 0
	} else {
		if alignment.run >= alignment.budget {
			return false
		}

		alignment.run++
		alignment.edited = true
	}

	if peer {
		alignment.peerIndex++
	}

	if source {
		alignment.sourceIndex++
		alignment.commitRow()
	}

	return true
}

func (alignment *lineAlignment) commitRow() {
	if alignment.peerIndex > 0 {
		alignment.end = alignment.peer.positions[alignment.peerIndex-1] + 1
	}

	if alignment.edited {
		alignment.level = SimilarSimilarityLevel
	}
}

func acceptedLinePositions(ctx context.Context, file *fileToCheck, start int, opts *Options) []int {
	if index := file.matchIndex(); index != nil {
		end := len(file.f.lines)
		if file.linesDone != nil {
			for line := start; line < end; line++ {
				if contextDone(ctx) || file.isDone(line) {
					end = line
					break
				}
			}
		}

		return index.positions[sort.SearchInts(index.positions, start):sort.SearchInts(index.positions, end)]
	}

	var positions []int

	for next := start; ; {
		line := nextAcceptedLine(ctx, file, next, opts)
		if line < 0 {
			break
		}

		positions = append(positions, line)
		next = line + 1
	}

	return positions
}

func alignPeerPrefix(ctx context.Context, source *fileToCheck, peer *FileOccurrence, positions []int, maximum int, opts *Options) (int, int, SimilarityLevel) {
	left := alignmentLines{file: source.f, positions: positions, index: source.matchIndex()}
	right := alignmentLines{file: peer.fileToCheck.f, positions: acceptedLinePositions(ctx, peer.fileToCheck, peer.End, opts), index: peer.fileToCheck.matchIndex()}
	alignment := lineAlignment{source: left, peer: right, maximum: maximum, budget: max(0, opts.MaxLineDistance), end: peer.End, level: EqualSimilarityLevel}
	blocks := matchingLineBlocks(ctx, left, right)

	blocks = append(blocks, lineMatch{source: len(positions), peer: len(right.positions)})
	for _, block := range blocks {
		if !alignment.gap(ctx, block.source-alignment.sourceIndex, block.peer-alignment.peerIndex) {
			break
		}

		for range block.count {
			if !alignment.take(ctx, true, true) {
				return alignment.result(ctx, peer.End)
			}
		}
	}

	return alignment.result(ctx, peer.End)
}

func (alignment *lineAlignment) gap(ctx context.Context, removed int, added int) bool {
	// Do not bridge an oversized ambiguous hunk by repeatedly re-pairing its
	// scaffolding. The chosen exact-line anchors define this entire edit gap.
	if max(removed, added) > alignment.budget {
		return false
	}

	paired := min(removed, added)
	for range paired {
		if !alignment.take(ctx, true, true) {
			return false
		}
	}

	for range removed - paired {
		if !alignment.take(ctx, true, false) {
			return false
		}
	}

	for range added - paired {
		if !alignment.take(ctx, false, true) {
			return false
		}
	}

	return true
}

func (alignment *lineAlignment) result(ctx context.Context, start int) (int, int, SimilarityLevel) {
	if contextDone(ctx) {
		return 0, start, EqualSimilarityLevel
	}

	return alignment.sourceIndex, alignment.end, alignment.level
}

func sourceExpansionLines(ctx context.Context, occs []*FileOccurrence, opts *Options) []int {
	source := occs[0]

	lastSeed, otherFile := source.Start, false
	for _, peer := range occs[1:] {
		lastSeed = max(lastSeed, peer.Start)
		otherFile = otherFile || peer.fileToCheck.f != source.fileToCheck.f
	}

	endLimit := len(source.fileToCheck.f.lines)
	if !otherFile {
		// Self-match seeds follow the source. Retain at least one disjoint
		// occurrence instead of growing the source across the final copy.
		endLimit = min(endLimit, lastSeed)
	}

	positions := acceptedLinePositions(ctx, source.fileToCheck, source.End, opts)
	positions = positions[:sort.SearchInts(positions, endLimit)]

	return positions
}

func (file *fileToCheck) matchIndex() *fileMatchIndex {
	if file.search == nil || file.search.files == nil {
		return nil
	}

	return file.search.files[file.fileID]
}

// Align one peer at a time. If a later peer shortens the common prefix,
// revisit earlier peers at that prefix. Keep only endpoints and levels for the
// group, not a separate alignment for every occurrence.
func expandLineOccurrences(ctx context.Context, occs []*FileOccurrence, level SimilarityLevel, opts *Options) SimilarityLevel {
	if len(occs) < 2 {
		return level
	}

	source := occs[0]

	positions := sourceExpansionLines(ctx, occs, opts)
	if len(positions) == 0 || contextDone(ctx) {
		return level
	}

	ends := make([]int, len(occs))
	levels := make([]SimilarityLevel, len(occs))
	levels[0] = level

	limit, revisit := len(positions), 0
	for idx, peer := range occs[1:] {
		rows, end, peerLevel := alignPeerPrefix(ctx, source.fileToCheck, peer, positions, limit, opts)
		if rows == 0 {
			return level
		}

		ends[idx+1], levels[idx+1] = end, peerLevel
		if rows < limit {
			limit, revisit = rows, idx
		}
	}

	for idx := 0; idx < revisit; idx++ {
		_, end, peerLevel := alignPeerPrefix(ctx, source.fileToCheck, occs[idx+1], positions, limit, opts)
		if contextDone(ctx) {
			return level
		}

		ends[idx+1], levels[idx+1] = end, peerLevel
	}

	ends[0] = positions[limit-1] + 1

	return commitLineOccurrences(ctx, occs, ends, levels, level)
}

func commitLineOccurrences(ctx context.Context, occs []*FileOccurrence, ends []int, levels []SimilarityLevel, level SimilarityLevel) SimilarityLevel {
	if contextDone(ctx) {
		return level
	}

	for idx, occ := range occs {
		occ.End = ends[idx]
		level = min(level, levels[idx])
	}

	markOccurrencesLinesDone(occs)

	return level
}
