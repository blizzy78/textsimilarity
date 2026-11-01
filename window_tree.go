package textsimilarity

import (
	"context"
	"math/bits"
	"runtime"
	"sync"
	"sync/atomic"
)

type windowNode struct {
	left, right uint32
	first, last uint32
	dimension   uint8
	cut         uint8
}
type windowTree struct {
	values    []uint64
	positions []uint64
	sketches  [][2]uint64
	nodes     []windowNode
	next      atomic.Uint32
}

// Windows are points in eight rune-length coordinates (first and last four
// lines). A valid match lies within +/- the edit limit in every coordinate.
// Clamping to 255 preserves that necessary bound. The tree finds candidates;
// original line comparisons and root-local markers still decide every match.

type windowBounds struct {
	packed, low, high uint64
	radius, limit     int
}

func widestWindowDimension(values []uint64) uint {
	var minimum, maximum [8]uint8
	for idx := range minimum {
		minimum[idx] = 255
	}

	step := max(1, len(values)/16)
	for idx := 0; idx < len(values); idx += step {
		for dim := uint(0); dim < 8; dim++ {
			part := uint8(values[idx] >> (dim * 8)) //nolint:gosec // extract one packed byte
			minimum[dim] = min(minimum[dim], part)
			maximum[dim] = max(maximum[dim], part)
		}
	}

	dimension, spread := uint(0), 0
	for dim := uint(0); dim < 8; dim++ {
		if value := int(maximum[dim]) - int(minimum[dim]); value > spread {
			dimension, spread = dim, value
		}
	}

	return dimension
}

func windowDimension(values []uint64) (uint, [256]uint32, int, int, bool) {
	preferred := widestWindowDimension(values)
	for attempt := uint(0); attempt < 8; attempt++ {
		dimension := (preferred + attempt) % 8

		var counts [256]uint32
		for _, packed := range values {
			counts[uint8(packed>>(dimension*8))]++ //nolint:gosec // extract one packed byte
		}

		lowerByte, upperByte := 0, 255
		for counts[lowerByte] == 0 {
			lowerByte++
		}

		for counts[upperByte] == 0 {
			upperByte--
		}

		if lowerByte != upperByte {
			return dimension, counts, lowerByte, upperByte, true
		}
	}

	return 0, [256]uint32{}, 0, 0, false
}

func (tree *windowTree) leafPossible(ctx context.Context, index *blockSearch, source *fileToCheck, start int, node windowNode, bounds *windowBounds, opts *Options) bool { //nolint:gocognit,cyclop // oversized uniform leaves and bounded bitmask leaves need separate traversals
	first, last := int(node.first), int(node.last)
	if last-first > 64 {
		// An oversized leaf has identical length signatures throughout.
		if incompatibleLengths(tree.values[first], bounds.packed, uint64(bounds.limit)) { //nolint:gosec // supported distances are 1..32
			return false
		}

		for position := first; position < last; position++ {
			if position&255 == 0 && contextDone(ctx) {
				return false
			}

			if tree.sketchPossible(position, bounds.low, bounds.high, bounds.radius) && tree.matches(index, source, start, position, opts) {
				return true
			}
		}

		return false
	}

	matches := matchingWindows(tree.values[first:last], bounds.packed, bounds.limit)
	for matches != 0 {
		offset := bits.TrailingZeros64(matches)

		matches &= matches - 1
		if tree.sketchPossible(first+offset, bounds.low, bounds.high, bounds.radius) && tree.matches(index, source, start, first+offset, opts) {
			return true
		}
	}

	return false
}

func newWindowTree(files []*File, span int) *windowTree {
	count := 0
	for _, file := range files {
		count += max(0, len(file.lines)-span+1)
	}

	tree := &windowTree{values: make([]uint64, count), positions: make([]uint64, count), sketches: make([][2]uint64, count), nodes: make([]windowNode, max(1, count*2))}
	position := 0

	for fileID, file := range files {
		if len(file.lines) < span {
			continue
		}

		var head, tail uint32
		for part := 0; part < 4; part++ {
			head |= uint32(min(file.lines[part].length, 255)) << uint(part*8)        //nolint:gosec // bounded packed values or explicitly selected benchmark input
			tail |= uint32(min(file.lines[span-4+part].length, 255)) << uint(part*8) //nolint:gosec // bounded packed values or explicitly selected benchmark input
		}

		for line := 0; line+span <= len(file.lines); line++ {
			tree.values[position] = uint64(head) | uint64(tail)<<32
			_, low, high := textSketch(file.lines[line].text)
			tree.sketches[position] = [2]uint64{low, high}
			tree.positions[position] = uint64(fileID)<<32 | uint64(line)
			position++

			if line+span < len(file.lines) {
				head = head>>8 | uint32(min(file.lines[line+4].length, 255))<<24 //nolint:gosec // bounded packed values or explicitly selected benchmark input
				tail = tail>>8 | uint32(min(file.lines[line+span].length, 255))<<24
			}
		}
	}

	tree.build(0, len(tree.values), runtime.GOMAXPROCS(0))
	tree.nodes = tree.nodes[:tree.next.Load()]

	return tree
}

func (tree *windowTree) build(first int, last int, threads int) uint32 {
	node := tree.next.Add(1) - 1
	tree.nodes[node].first = uint32(first) //nolint:gosec // caller bounds the total window count

	tree.nodes[node].last = uint32(last) //nolint:gosec // caller bounds the total window count
	if last-first <= 64 {
		return node
	}

	dimension, counts, lowerByte, upperByte, found := windowDimension(tree.values[first:last])
	if !found {
		return node
	}

	cut, total := lowerByte, 0
	for cut < upperByte {
		total += int(counts[cut])
		if total >= (last-first)/2 {
			break
		}

		cut++
	}

	if cut == lowerByte {
		cut++
	}

	lower, upper := first, last
	for lower < upper {
		if int(uint8(tree.values[lower]>>(dimension*8))) < cut { //nolint:gosec // bounded packed values or explicitly selected benchmark input
			lower++
		} else {
			upper--
			tree.values[lower], tree.values[upper] = tree.values[upper], tree.values[lower]
			tree.positions[lower], tree.positions[upper] = tree.positions[upper], tree.positions[lower]
			tree.sketches[lower], tree.sketches[upper] = tree.sketches[upper], tree.sketches[lower]
		}
	}

	middle := lower

	var left, right uint32

	if threads > 1 && last-first > 4096 {
		var group sync.WaitGroup
		group.Go(func() { right = tree.build(middle, last, threads/2) })

		left = tree.build(first, middle, threads-threads/2)

		group.Wait()
	} else {
		left = tree.build(first, middle, 1)
		right = tree.build(middle, last, 1)
	}

	tree.nodes[node].left = left
	tree.nodes[node].right = right
	tree.nodes[node].dimension = uint8(dimension) //nolint:gosec // bounded packed values or explicitly selected benchmark input
	tree.nodes[node].cut = uint8(cut)             //nolint:gosec // bounded packed values or explicitly selected benchmark input

	return node
}

func (tree *windowTree) possible(ctx context.Context, index *blockSearch, source *fileToCheck, start int, limit int, opts *Options) bool { //nolint:gocognit,cyclop // bounded byte coordinates and an explicit traversal stack
	var (
		lower, upper [8]uint8
		packed       uint64
	)

	for part := 0; part < 8; part++ {
		offset := part
		if part >= 4 {
			offset = index.span - 4 + part - 4
		}

		length := min(source.f.lines[start+offset].length, 255)
		lower[part] = uint8(max(0, length-limit))   //nolint:gosec // bounded packed values or explicitly selected benchmark input
		upper[part] = uint8(min(255, length+limit)) //nolint:gosec // bounded packed values or explicitly selected benchmark input
		packed |= uint64(length) << uint(part*8)    //nolint:gosec // bounded packed values or explicitly selected benchmark input
	}

	_, sketchLow, sketchHigh := textSketch(source.f.lines[start].text)

	radius := 2 * limit
	if source.f.lines[start].length <= limit {
		radius = 128
	}

	var local [64]uint32

	stack := local[:1]
	stack[0] = 0

	steps := 0
	for len(stack) != 0 {
		if steps&63 == 0 && contextDone(ctx) {
			return false
		}

		steps++
		node := tree.nodes[stack[len(stack)-1]]
		stack = stack[:len(stack)-1]

		if node.left == 0 {
			bounds := windowBounds{packed: packed, low: sketchLow, high: sketchHigh, radius: radius, limit: limit}
			if tree.leafPossible(ctx, index, source, start, node, &bounds, opts) {
				return true
			}

			continue
		}

		if lower[node.dimension] < node.cut {
			stack = append(stack, node.left)
		}

		if upper[node.dimension] >= node.cut {
			stack = append(stack, node.right)
		}
	}

	return false
}

func (tree *windowTree) matches(index *blockSearch, source *fileToCheck, start int, position int, opts *Options) bool {
	code := tree.positions[position]

	file, line := uint32(code>>32), uint32(code&0xffffffff)
	if index.files[file] == source.f && int(line) <= start || source.peerDone(int(file), int(line)) {
		return false
	}

	return index.matchesBlock(source, start, blockLocation{file: file, line: line}, opts)
}

func (tree *windowTree) sketchPossible(position int, low uint64, high uint64, radius int) bool {
	sketch := tree.sketches[position]
	return bits.OnesCount64(sketch[0]^low)+bits.OnesCount64(sketch[1]^high) <= radius
}
