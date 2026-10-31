package textsimilarity

import (
	"context"
	"math/bits"
)

type blockLocation struct {
	next uint32
	file uint32
	line uint32
}

type terminalGroup struct {
	line      *fileLine
	locations []blockLocation
}

// blockSearch uses four necessary rune-length bounds to find candidate blocks.
// Quantization and hashing only add candidates; complete matching still decides.
// Each Similarities call rebuilds it in memory from the supplied readers.
type blockSearch struct {
	files     []*File
	span      int
	positions [4]int
	keys      []uint32
	heads     []uint32
	locations []blockLocation
	shift     uint
	lengths   [][]int32
	ends      []terminalGroup
	maxEnd    int
}

func blockWidth(opts *Options) int {
	limit := opts.MaxEditDistance
	if limit <= 0 {
		limit = DefaultMaxEditDistance
	}

	if opts.MinSimilarLines < 4 || opts.MinSimilarLines > 64 || limit > 32 || opts.MinLineLength > 0 || opts.flagSet(IgnoreBlankLinesFlag) || opts.IgnoreLineRegex != nil {
		return 0
	}

	return 2*limit + 1
}

func newBlockSearch(files []*File, opts *Options) *blockSearch { //nolint:gocognit,cyclop // validate bounds before constructing compact postings and EOF groups
	if blockWidth(opts) == 0 || uint64(len(files)) > 1<<32-1 {
		return nil
	}

	span := opts.MinSimilarLines
	count := 0

	for _, file := range files {
		windows := max(0, len(file.lines)-span+1)
		if windows > 1<<24-count {
			return nil
		}

		for _, line := range file.lines {
			if int64(line.length) > 1<<31-1 {
				return nil
			}
		}

		count += windows
	}

	size := 256
	for size < count*2 {
		size *= 2
	}

	index := &blockSearch{files: files, span: span, positions: [4]int{0, span / 3, 2 * span / 3, span - 1}, keys: make([]uint32, size), heads: make([]uint32, size), locations: make([]blockLocation, count), shift: 32 - uint(bits.TrailingZeros(uint(size)))}
	position := 0
	index.lengths = make([][]int32, len(files))

	endGroups := map[string]int{}

	for fileID, file := range files {
		index.lengths[fileID] = make([]int32, len(file.lines))
		for idx, line := range file.lines {
			index.lengths[fileID][idx] = int32(line.length) //nolint:gosec // lengths were checked against MaxInt32 above
		}

		if len(file.lines) != 0 {
			last := len(file.lines) - 1
			line := file.lines[last]

			group, ok := endGroups[line.text]
			if !ok {
				group = len(index.ends)
				endGroups[line.text] = group
				index.ends = append(index.ends, terminalGroup{line: line})
				index.maxEnd = max(index.maxEnd, line.length)
			}

			index.ends[group].locations = append(index.ends[group].locations, blockLocation{file: uint32(fileID), line: uint32(last)}) //nolint:gosec // file count and per-file window count were bounded above
		}

		for line := 0; line+span <= len(file.lines); line++ {
			var key uint32
			for part, offset := range index.positions {
				key |= uint32(file.lines[line+offset].bucket) << uint(part*8)
			}

			slot := index.slot(key)
			index.keys[slot] = key
			index.locations[position] = blockLocation{next: index.heads[slot], file: uint32(fileID), line: uint32(line)}
			position++
			index.heads[slot] = uint32(position)
		}
	}

	return index
}

func (index *blockSearch) slot(key uint32) uint32 {
	position := key * 0x9e3779b1 >> index.shift
	for index.heads[position] != 0 && index.keys[position] != key {
		position = (position + 1) & uint32(len(index.keys)-1) //nolint:gosec // at most 2^25 slots
	}

	return position
}

func (index *blockSearch) possible(ctx context.Context, source *fileToCheck, start int, opts *Options) bool { //nolint:gocognit,cyclop // candidate traversal checks cancellation, length bounds, and independent marker states
	if index == nil {
		return true
	}

	if start+index.span > len(source.f.lines) {
		return false
	}

	limit := opts.MaxEditDistance
	if limit <= 0 {
		limit = DefaultMaxEditDistance
	}

	if index.matchesEnd(source, start, limit, opts) {
		return false
	}

	var sourceLengths [64]int32

	for offset := 0; offset < index.span; offset++ {
		line := source.f.lines[start+offset]
		if source.isDone(start+offset) || line.flagSet(matchesAlwaysDifferentLineFlag) {
			return false
		}

		sourceLengths[offset] = int32(line.length) //nolint:gosec // index construction validated every length
	}

	var lower, upper [4]byte

	for part, offset := range index.positions {
		line := source.f.lines[start+offset]
		lower[part], upper[part] = line.lower, line.upper
	}

	for selection := 0; selection < 16; selection++ {
		key, duplicate := blockKey(lower, upper, selection)
		if duplicate {
			continue
		}

		for candidate := index.heads[index.slot(key)]; candidate != 0; {
			if contextDone(ctx) {
				return false
			}

			location := index.locations[candidate-1]
			candidate = location.next
			peerFile := index.files[location.file]

			peerStart := int(location.line)
			if peerFile == source.f && peerStart <= start || source.peerDone(int(location.file), peerStart) {
				continue
			}

			peerLengths := index.lengths[location.file][peerStart : peerStart+index.span]
			if !blockLengthsMatch(sourceLengths[:index.span], peerLengths, int32(limit)) {
				continue
			}

			if index.matchesBlock(source, start, location, opts) {
				return true
			}
		}
	}

	return false
}

// All seed occurrences participate in expansion. An unmarked occurrence at EOF
// prevents that entire group from expanding even one line.
func (index *blockSearch) matchesEnd(source *fileToCheck, start int, limit int, opts *Options) bool {
	needle := source.f.lines[start]
	if needle.length-index.maxEnd > limit {
		return false
	}

	for _, group := range index.ends {
		if linesSimilarity(needle, group.line, opts) == differentSimilarityLevel {
			continue
		}

		for _, location := range group.locations {
			if index.files[location.file] == source.f && int(location.line) <= start || source.peerDone(int(location.file), int(location.line)) {
				continue
			}

			return true
		}
	}

	return false
}

func blockKey(lower [4]byte, upper [4]byte, selection int) (uint32, bool) {
	var key uint32

	for part := range 4 {
		value := lower[part]
		if selection&(1<<part) != 0 {
			if lower[part] == upper[part] {
				return 0, true
			}

			value = upper[part]
		}

		key |= uint32(value) << uint(part*8)
	}

	return key, false
}

func (index *blockSearch) matchesBlock(source *fileToCheck, start int, location blockLocation, opts *Options) bool {
	peer := index.files[location.file]

	peerStart := int(location.line)
	for offset := 0; offset < index.span; offset++ {
		if source.peerDone(int(location.file), peerStart+offset) || linesSimilarity(source.f.lines[start+offset], peer.lines[peerStart+offset], opts) == differentSimilarityLevel {
			return false
		}
	}

	return true
}
