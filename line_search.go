package textsimilarity

import (
	"context"
	"math/bits"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
)

type searchLine struct {
	position uint64
	line     *fileLine
	group    int
}
type searchCandidate struct {
	line            *fileLine
	positions       []uint64
	mask, low, high uint64
	witnesses       []uint64
}
type lineBucket struct {
	raw        []searchLine
	candidates []searchCandidate
	sketches   [][2]uint64
	queries    []atomic.Pointer[lineQuery]
}
type lineSearch struct {
	buckets   []lineBucket
	fileCount int
	blocks    *blockSearch
}
type lineQuery struct {
	once       sync.Once
	rangesOnce sync.Once
	hits       []lineHit
	ranges     []uint32
	files      []int
}
type lineHit struct {
	position uint64
	level    SimilarityLevel
}

func indexedLineCount(files []*File) (int, int) {
	maximum, total := 0, 0

	for _, file := range files {
		for _, line := range file.lines {
			if line.length <= 65536 {
				total++
				maximum = max(maximum, line.length)
			}
		}
	}

	return maximum, total
}

// These exact seed occurrences all participate in expansion. The shortest
// remaining suffix and the length extremes at each offset expose contradictions
// cheaply. Every witness is rechecked against the root's current markers.
func (candidate *searchCandidate) prepareWitnesses(files []*File, span int, limit int) {
	if len(candidate.positions) < 2 || candidate.line.length <= limit {
		return
	}

	candidate.witnesses = make([]uint64, 1+2*(span-1))

	first := candidate.positions[0]
	for idx := range candidate.witnesses {
		candidate.witnesses[idx] = first
	}

	remaining := func(pos uint64) int { return len(files[pos>>32].lines) - int(pos&0xffffffff) }
	for _, pos := range candidate.positions {
		if remaining(pos) < remaining(candidate.witnesses[0]) {
			candidate.witnesses[0] = pos
		}
	}

	for offset := 1; offset < span; offset++ {
		candidate.witnesses[2*offset-1], candidate.witnesses[2*offset] = candidate.extremes(files, offset)
	}
}

func (candidate *searchCandidate) extremes(files []*File, offset int) (uint64, uint64) {
	shortest, longest := candidate.positions[0], candidate.positions[0]
	minimum, maximum := int(^uint(0)>>1), -1

	for _, pos := range candidate.positions {
		lines, line := files[pos>>32].lines, int(pos&0xffffffff)
		if line+offset >= len(lines) {
			continue
		}

		length := lines[line+offset].length
		if length < minimum {
			minimum, shortest = length, pos
		}

		if length > maximum {
			maximum, longest = length, pos
		}
	}

	return shortest, longest
}

func (bucket *lineBucket) appendMatches(ctx context.Context, query *lineQuery, needle *searchCandidate, limit int, opts *Options) {
	radius := 2 * limit

	if len(bucket.candidates) == 0 {
		return
	}

	if max(bucket.candidates[0].line.length, needle.line.length) <= limit {
		// The public predicate treats any two sufficiently short lines as
		// similar. Its signature lower bounds do not apply to that shortcut.
		radius = 128
	}

	indices := matchingSignatures(bucket.sketches, needle.low, needle.high, radius)
	for _, idx := range indices {
		if contextDone(ctx) {
			return
		}

		candidate := &bucket.candidates[idx]
		if bits.OnesCount64(needle.mask&^candidate.mask) > limit || bits.OnesCount64(candidate.mask&^needle.mask) > limit {
			continue
		}

		level := linesSimilarity(candidate.line, needle.line, opts)
		if level == differentSimilarityLevel {
			continue
		}

		for _, position := range candidate.positions {
			query.hits = append(query.hits, lineHit{position: position, level: level})
		}
	}
}

func canExpandAllSeeds(ctx context.Context, source *fileToCheck, start int, query *lineQuery, opts *Options) bool {
	for _, hit := range query.hits {
		if contextDone(ctx) {
			return false
		}

		file, line := int(hit.position>>32), int(hit.position&0xffffffff)

		peer := source.peerAt(file)
		if peer.f == source.f && line <= start || peer.isDone(line) {
			continue
		}

		if !canReachMinimum(ctx, source, start, peer, line, opts) {
			return false
		}
	}

	return true
}

func newLineSearch(files []*File, opts *Options) *lineSearch {
	if blockWidth(opts) == 0 {
		return nil
	}

	maximum, total := indexedLineCount(files)

	if total > 2000000 {
		return nil
	}

	search := &lineSearch{buckets: make([]lineBucket, maximum+1), fileCount: len(files)}
	counts := make([]int, maximum+1)

	for _, file := range files {
		for _, line := range file.lines {
			if line.length > maximum {
				continue
			}

			counts[line.length]++
		}
	}

	raw := make([]searchLine, total)

	position := 0
	for length, count := range counts {
		search.buckets[length].raw = raw[position : position+count : position+count]
		position += count
		counts[length] = 0
	}

	scatterSearchLines(files, search.buckets, counts)

	var workers sync.WaitGroup

	limit := opts.MaxEditDistance
	if limit <= 0 {
		limit = DefaultMaxEditDistance
	}

	threads := min(runtime.GOMAXPROCS(0), len(search.buckets))
	for worker := 0; worker < threads; worker++ {
		workers.Go(func() {
			for length := worker; length < len(search.buckets); length += threads {
				search.buckets[length].prepare(files, opts.MinSimilarLines, limit)
			}
		})
	}

	workers.Wait()

	return search
}

func (bucket *lineBucket) prepare(files []*File, span int, limit int) {
	if len(bucket.raw) == 0 {
		return
	}

	groups := make(map[string]int, min(len(bucket.raw), 512))
	for idx := range bucket.raw {
		entry := &bucket.raw[idx]

		group, found := groups[entry.line.text]
		if !found {
			group = len(groups)
			groups[entry.line.text] = group
		}

		entry.group = group
	}

	bucket.candidates = make([]searchCandidate, len(groups))
	bucket.queries = make([]atomic.Pointer[lineQuery], len(groups))
	bucket.sketches = make([][2]uint64, len(groups))

	counts := make([]int, len(groups))
	for _, entry := range bucket.raw {
		counts[entry.group]++
		bucket.candidates[entry.group].line = entry.line
	}

	positions := make([]uint64, len(bucket.raw))
	start := 0

	for idx := range bucket.candidates {
		candidate := &bucket.candidates[idx]
		count := counts[idx]
		candidate.positions = positions[start : start+count : start+count]
		start += count
		counts[idx] = 0
		candidate.mask, candidate.low, candidate.high = textSketch(candidate.line.text)
		bucket.sketches[idx] = [2]uint64{candidate.low, candidate.high}
	}

	for _, entry := range bucket.raw {
		bucket.candidates[entry.group].positions[counts[entry.group]] = entry.position
		counts[entry.group]++
		entry.line.groupID = uint32(entry.group) //nolint:gosec // at most two million indexed lines
	}

	for idx := range bucket.candidates {
		bucket.candidates[idx].prepareWitnesses(files, span, limit)
	}

	bucket.raw = nil
}

func textSketch(text string) (uint64, uint64, uint64) {
	// Hashing runes into 64 counters can only weaken an edit-distance bound.
	// Store each count modulo four in Gray code: an insertion/deletion changes
	// one bit, and a substitution changes at most two. Hamming distance / 2
	// therefore never exceeds Levenshtein distance. Verify survivors exactly.
	var mask, low, high uint64

	for _, char := range text {
		bit := uint64(1) << uint32(char&63)
		mask |= bit
		high ^= low & bit
		low ^= bit
	}

	return mask, low ^ high, high
}

func (search *lineSearch) find(ctx context.Context, file *fileToCheck, needle *fileLine, start int, opts *Options) (int, SimilarityLevel) {
	if contextDone(ctx) {
		return -1, differentSimilarityLevel
	}

	query := search.query(ctx, needle, opts)
	if contextDone(ctx) || len(query.hits) == 0 {
		return -1, differentSimilarityLevel
	}

	query.rangesOnce.Do(func() {
		query.ranges = make([]uint32, search.fileCount+1)
		for _, hit := range query.hits {
			query.ranges[int(hit.position>>32)+1]++
		}

		for idx := 1; idx < len(query.ranges); idx++ {
			query.ranges[idx] += query.ranges[idx-1]
		}
	})
	hits := query.hits[query.ranges[file.fileID]:query.ranges[file.fileID+1]]
	position := uint64(file.fileID)<<32 | uint64(start) //nolint:gosec // internal nonnegative file and line ordinals

	first := sort.Search(len(hits), func(idx int) bool { return hits[idx].position >= position })
	for _, hit := range hits[first:] {
		if hit.position>>32 != uint64(file.fileID) { //nolint:gosec // internal nonnegative file ordinal
			break
		}

		line := int(hit.position & 0xffffffff)
		if !file.isDone(line) {
			return line, hit.level
		}
	}

	return -1, differentSimilarityLevel
}

func (search *lineSearch) query(ctx context.Context, needle *fileLine, opts *Options) *lineQuery {
	entry := &search.buckets[needle.length].queries[needle.groupID]

	query := entry.Load()
	if query == nil {
		query = &lineQuery{}
		if !entry.CompareAndSwap(nil, query) {
			query = entry.Load()
		}
	}

	query.once.Do(func() {
		if needle.flagSet(matchesAlwaysDifferentLineFlag) {
			return
		}

		limit := opts.MaxEditDistance
		if limit <= 0 {
			limit = DefaultMaxEditDistance
		}

		prepared := search.prepared(needle)
		for length := max(0, needle.length-limit); length <= min(len(search.buckets)-1, needle.length+limit); length++ {
			search.buckets[length].appendMatches(ctx, query, prepared, limit, opts)
		}

		search.finishQuery(query)
	})

	return query
}

func (search *lineSearch) finishQuery(query *lineQuery) {
	sort.Slice(query.hits, func(i int, j int) bool { return query.hits[i].position < query.hits[j].position })

	for _, hit := range query.hits {
		fileID := int(hit.position >> 32)
		if len(query.files) == 0 || query.files[len(query.files)-1] != fileID {
			query.files = append(query.files, fileID)
		}
	}

	if len(query.hits) == 1 && search.blocks != nil && search.blocks.dead != nil {
		position := query.hits[0].position
		file, line := int(position>>32), int(position&0xffffffff)

		span := search.blocks.span
		for start := max(0, line-span+1); start <= min(line, len(search.blocks.dead[file])-span); start++ {
			search.blocks.dead[file][start].Store(1)
		}
	}
}

func matchingSignatures(signatures [][2]uint64, low uint64, high uint64, radius int) []int {
	var matches []int

	for idx, signature := range signatures {
		if bits.OnesCount64(low^signature[0])+bits.OnesCount64(high^signature[1]) <= radius {
			matches = append(matches, idx)
		}
	}

	return matches
}

func (search *lineSearch) prepared(line *fileLine) *searchCandidate {
	if line.length >= len(search.buckets) {
		return nil
	}

	return &search.buckets[line.length].candidates[line.groupID]
}

func (search *lineSearch) hasIndex(line *fileLine) bool {
	return line.length < len(search.buckets) && line.length <= 65536-32
}

func (search *lineSearch) compare(left *fileLine, right *fileLine, opts *Options) SimilarityLevel {
	if left.text == right.text {
		return linesSimilarity(left, right, opts)
	}

	limit := opts.MaxEditDistance
	if limit <= 0 {
		limit = DefaultMaxEditDistance
	}

	if max(left.length, right.length) > limit {
		a, b := search.prepared(left), search.prepared(right)
		if a != nil && b != nil {
			changes := bits.OnesCount64(a.low^b.low) + bits.OnesCount64(a.high^b.high)
			if changes > 2*limit || bits.OnesCount64(a.mask&^b.mask) > limit || bits.OnesCount64(b.mask&^a.mask) > limit {
				return differentSimilarityLevel
			}
		}
	}

	return linesSimilarity(left, right, opts)
}

func scatterSearchLines(files []*File, buckets []lineBucket, counts []int) {
	maximum := len(buckets) - 1

	for fileID, file := range files {
		for idx, line := range file.lines {
			length := line.length
			if length > maximum {
				continue
			}

			buckets[length].raw[counts[length]] = searchLine{position: uint64(fileID)<<32 | uint64(idx), line: line}
			counts[length]++
		}
	}
}
