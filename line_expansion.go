package textsimilarity

import "context"

type lineCell struct {
	distance int
	level    SimilarityLevel
}

// Keep the least consecutive edit count at each reachable peer prefix. A smaller
// count dominates larger counts for every continuation, including exact resets.
// Offsets can accumulate across resets, so a fixed diagonal band is not valid.
// Only two trimmed rows are retained, bounded by the peer's input size.
type lineAlignment struct {
	peer      *fileToCheck
	start     int
	next      int
	positions []int
	ended     bool
	limit     int
	lower     int
	previous  []lineCell
	current   []lineCell
}

func newLineAlignment(peer *fileToCheck, start int, source *fileToCheck, opts *Options) *lineAlignment {
	limit := min(max(0, opts.MaxLineDistance), max(len(source.f.lines), len(peer.f.lines)))
	return &lineAlignment{peer: peer, start: start, next: start, limit: limit}
}

func (alignment *lineAlignment) readThrough(ctx context.Context, column int, opts *Options) {
	for len(alignment.positions) < column && !alignment.ended {
		line := nextAcceptedLine(ctx, alignment.peer, alignment.next, opts)
		if line < 0 {
			alignment.ended = true
			return
		}

		alignment.positions = append(alignment.positions, line)
		alignment.next = line + 1
	}
}

func (alignment *lineAlignment) cell(column int) lineCell {
	index := column - alignment.lower
	if index < 0 || index >= len(alignment.previous) {
		return lineCell{distance: alignment.limit + 1}
	}

	return alignment.previous[index]
}

func betterLineCell(left lineCell, right lineCell) lineCell {
	if left.distance < right.distance || left.distance == right.distance && left.level > right.level {
		return left
	}

	return right
}

func validLinePrefix(cell lineCell, best lineCell, limit int) bool {
	return cell.distance <= limit && (cell.distance < best.distance || cell.distance == best.distance && cell.level >= best.level)
}

func editedLineCell(cell lineCell) lineCell {
	return lineCell{distance: cell.distance + 1, level: SimilarSimilarityLevel}
}

func (alignment *lineAlignment) initialize(ctx context.Context, opts *Options) {
	alignment.readThrough(ctx, alignment.limit, opts)
	capacity := min(2*alignment.limit+1, len(alignment.peer.f.lines)-alignment.start+1)
	alignment.previous = make([]lineCell, len(alignment.positions)+1, capacity)

	alignment.current = make([]lineCell, 0, capacity)
	for column := range alignment.previous {
		alignment.previous[column] = lineCell{distance: column, level: SimilarSimilarityLevel}
	}

	alignment.previous[0].level = EqualSimilarityLevel
}

func (alignment *lineAlignment) nextCell(source *fileToCheck, line int, column int, lower int) lineCell {
	cell := editedLineCell(alignment.cell(column)) // Remove this source line.
	if column > lower {
		cell = betterLineCell(cell, editedLineCell(alignment.current[column-lower-1]))
	}

	if column == 0 {
		return cell
	}

	diagonal := alignment.cell(column - 1)
	if diagonal.distance > alignment.limit {
		return cell
	}

	level := linesSimilarity(source.f.lines[line], alignment.peer.f.lines[alignment.positions[column-1]])
	if level == differentSimilarityLevel {
		diagonal = editedLineCell(diagonal) // Replace a line, costing one edit.
	} else {
		diagonal.distance = 0 // Only exact matches reset the consecutive edit counter.
	}

	return betterLineCell(cell, diagonal)
}

// advance returns the peer prefix with the smallest trailing edit run. On a
// tie, prefer an equal alignment, then the longer peer prefix. Endpoints are not
// committed while exploring; later rows may select a different earlier path.
func (alignment *lineAlignment) advance(ctx context.Context, source *fileToCheck, line int, opts *Options) (int, SimilarityLevel, bool) {
	if alignment.previous == nil {
		alignment.initialize(ctx, opts)
	}

	// A diagonal exact match can reset the counter, permitting another limit
	// insertions in this row. Read no farther than that reachable prefix.
	previousEnd := alignment.lower + len(alignment.previous)
	lookahead := min(len(alignment.peer.f.lines)-alignment.start, previousEnd+alignment.limit)
	alignment.readThrough(ctx, lookahead, opts)
	lower := alignment.lower

	upper := min(len(alignment.positions), lookahead)
	if lower > upper || contextDone(ctx) {
		return 0, differentSimilarityLevel, false
	}

	width := upper - lower + 1
	if cap(alignment.current) < width {
		alignment.current = make([]lineCell, width)
	}

	alignment.current = alignment.current[:width]
	best := lineCell{distance: alignment.limit + 1}
	bestColumn := 0

	for column := lower; column <= upper; column++ {
		if contextDone(ctx) {
			return 0, differentSimilarityLevel, false
		}

		cell := alignment.nextCell(source, line, column, lower)

		alignment.current[column-lower] = cell
		if validLinePrefix(cell, best, alignment.limit) {
			best, bestColumn = cell, column
		}
	}

	if best.distance > alignment.limit {
		return 0, differentSimilarityLevel, false
	}

	alignment.commitRow(lower)

	end := alignment.start
	if bestColumn > 0 {
		end = alignment.positions[bestColumn-1] + 1
	}

	return end, best.level, true
}

func (alignment *lineAlignment) commitRow(lower int) {
	first, last := 0, len(alignment.current)-1
	for alignment.current[first].distance > alignment.limit {
		first++
	}

	for alignment.current[last].distance > alignment.limit {
		last--
	}
	// Compact in place; slicing off the front would lose reusable capacity.
	copy(alignment.current, alignment.current[first:last+1])
	alignment.current = alignment.current[:last-first+1]
	alignment.previous, alignment.current = alignment.current, alignment.previous
	alignment.lower = lower + first
}

func canAlignMinimum(ctx context.Context, source *fileToCheck, sourceStart int, peer *fileToCheck, peerStart int, opts *Options) bool {
	if len(source.f.lines)-sourceStart < opts.MinSimilarLines {
		return false
	}

	alignment := newLineAlignment(peer, peerStart+1, source, opts)

	end := sourceStart + 1
	for end-sourceStart < opts.MinSimilarLines {
		line := nextAcceptedLine(ctx, source, end, opts)
		if line < 0 {
			return false
		}

		if _, _, possible := alignment.advance(ctx, source, line, opts); !possible {
			return false
		}

		end = line + 1
	}

	return true
}

func sourceExpansionLines(ctx context.Context, occs []*FileOccurrence, opts *Options) []int {
	source := occs[0]
	maximum := len(source.fileToCheck.f.lines) - source.End

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

	var positions []int
	for next := source.End; len(positions) < maximum; {
		line := nextAcceptedLine(ctx, source.fileToCheck, next, opts)
		if line < 0 || line >= endLimit {
			break
		}

		positions = append(positions, line)
		next = line + 1
	}

	return positions
}

func alignPeerPrefix(ctx context.Context, source *fileToCheck, peer *FileOccurrence, positions []int, opts *Options) (int, int, SimilarityLevel) {
	alignment := newLineAlignment(peer.fileToCheck, peer.End, source, opts)
	end, level := peer.End, EqualSimilarityLevel
	rows := 0

	for _, line := range positions {
		peerEnd, peerLevel, possible := alignment.advance(ctx, source, line, opts)
		if !possible {
			break
		}

		end, level = peerEnd, peerLevel
		rows++
	}

	if contextDone(ctx) {
		return 0, peer.End, EqualSimilarityLevel
	}

	return rows, end, level
}

// Stream one peer's frontier at a time. If a later peer shortens the common prefix,
// revisit earlier peers at that prefix. Keep only endpoints and levels for the
// group, not a separate dynamic-programming frontier for every occurrence.
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
		rows, end, peerLevel := alignPeerPrefix(ctx, source.fileToCheck, peer, positions[:limit], opts)
		if rows == 0 {
			return level
		}

		ends[idx+1], levels[idx+1] = end, peerLevel
		if rows < limit {
			limit, revisit = rows, idx
		}
	}

	for idx := 0; idx < revisit; idx++ {
		_, end, peerLevel := alignPeerPrefix(ctx, source.fileToCheck, occs[idx+1], positions[:limit], opts)
		if contextDone(ctx) {
			return level
		}

		ends[idx+1], levels[idx+1] = end, peerLevel
	}

	ends[0] = positions[limit-1] + 1
	for idx, occ := range occs {
		occ.End = ends[idx]
		level = min(level, levels[idx])
	}

	markOccurrencesLinesDone(occs)

	return level
}
