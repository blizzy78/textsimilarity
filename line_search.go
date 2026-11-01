package textsimilarity

import (
	"context"
	"math/bits"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
)

const (
	maxLineQueryHits  = 4096
	maxLineQueryBytes = 64 << 20
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
	buckets    []lineBucket
	blocks     *blockSearch
	shortTails *blockSearch
	queryBytes atomic.Int64
}
type lineQuery struct {
	once     sync.Once
	hits     []lineHit
	files    []int
	fallback bool
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
	if span < 1 || len(candidate.positions) < 2 || candidate.line.length <= limit {
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

	for idx, signature := range bucket.sketches {
		if contextDone(ctx) {
			return
		}

		if bits.OnesCount64(needle.low^signature[0])+bits.OnesCount64(needle.high^signature[1]) > radius {
			continue
		}

		candidate := &bucket.candidates[idx]
		if bits.OnesCount64(needle.mask&^candidate.mask) > limit || bits.OnesCount64(candidate.mask&^needle.mask) > limit {
			continue
		}

		level := linesSimilarity(candidate.line, needle.line, opts)
		if level == differentSimilarityLevel {
			continue
		}

		if !query.appendHits(candidate.positions, level) {
			return
		}
	}
}

func (query *lineQuery) appendHits(positions []uint64, level SimilarityLevel) bool {
	if len(positions) > maxLineQueryHits-len(query.hits) {
		query.hits = nil
		query.fallback = true

		return false
	}

	for _, position := range positions {
		query.hits = append(query.hits, lineHit{position: position, level: level})
	}

	return true
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

		if opts.MaxLineDistance > 0 {
			// Indexed scans have no skipped lines. Deletions cannot bridge a
			// suffix shorter than this necessary minimum.
			if len(peer.f.lines)-line < opts.MinSimilarLines-opts.MaxLineDistance {
				return false
			}
		} else if !canReachMinimum(ctx, source, start, peer, line, opts) {
			return false
		}
	}

	return true
}

func newLineSearch(files []*File, opts *Options) *lineSearch { //nolint:cyclop // bounded bucket preparation, scattering, and worker setup
	if lineIndexWidth(opts) == 0 {
		return nil
	}

	maximum, total := indexedLineCount(files)

	if total > 2000000 {
		return nil
	}

	search := &lineSearch{buckets: make([]lineBucket, maximum+1)}
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

	witnessSpan := opts.MinSimilarLines
	if opts.MaxLineDistance > 0 {
		witnessSpan = 0
	}

	for worker := 0; worker < threads; worker++ {
		workers.Go(func() {
			for length := worker; length < len(search.buckets); length += threads {
				search.buckets[length].prepare(files, witnessSpan, limit)
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
	if query.fallback {
		return linearLineIndex(ctx, file, needle, start, opts)
	}

	if contextDone(ctx) || len(query.hits) == 0 {
		return -1, differentSimilarityLevel
	}

	position := uint64(file.fileID)<<32 | uint64(start) //nolint:gosec // internal nonnegative file and line ordinals

	first := sort.Search(len(query.hits), func(idx int) bool { return query.hits[idx].position >= position })
	for _, hit := range query.hits[first:] {
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

			if query.fallback {
				return
			}
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

	if !search.reserveQuery(query) {
		query.hits, query.files = nil, nil
		query.fallback = true

		return
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

func (search *lineSearch) reserveQuery(query *lineQuery) bool {
	// Upper bounds for both 32-bit and 64-bit Go. Input and query metadata are
	// linear in input size; cap the potentially quadratic cached match lists.
	bytes := int64(cap(query.hits))*16 + int64(cap(query.files))*8

	for {
		used := search.queryBytes.Load()
		if bytes > maxLineQueryBytes-used {
			return false
		}

		if search.queryBytes.CompareAndSwap(used, used+bytes) {
			return true
		}
	}
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
