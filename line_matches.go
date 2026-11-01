package textsimilarity

import (
	"context"
	"sort"
)

type alignmentLines struct {
	file      *File
	positions []int
	index     *fileMatchIndex
}

type fileMatchIndex struct {
	lines     map[string][]int
	positions []int
}

func (lines alignmentLines) at(index int) *fileLine {
	return lines.file.lines[lines.positions[index]]
}

type lineMatch struct {
	source int
	peer   int
	count  int
}

type lineMatchTask struct {
	sourceStart int
	sourceEnd   int
	peerStart   int
	peerEnd     int
	matched     bool
}

// Find a patience-diff alignment: exact prefixes/suffixes and increasing,
// unique common lines provide synchronization points. An ambiguous middle
// without such anchors remains a single changed gap, rather than being
// repeatedly synchronized by coincidental scaffolding.
func matchingLineBlocks(ctx context.Context, source alignmentLines, peer alignmentLines) []lineMatch {
	source = source.withIndex(ctx)
	peer = peer.withIndex(ctx)
	tasks := []lineMatchTask{{sourceEnd: len(source.positions), peerEnd: len(peer.positions)}}

	var blocks []lineMatch

	for len(tasks) != 0 {
		if contextDone(ctx) {
			return nil
		}

		task := tasks[len(tasks)-1]
		tasks = tasks[:len(tasks)-1]

		if task.matched {
			blocks = appendLineMatch(blocks, lineMatch{source: task.sourceStart, peer: task.peerStart, count: task.sourceEnd - task.sourceStart})
			continue
		}

		var prefix, suffix lineMatch

		task, prefix, suffix = trimLineMatchTask(ctx, source, peer, task)
		if contextDone(ctx) {
			return nil
		}

		if prefix.count > 0 {
			blocks = appendLineMatch(blocks, prefix)
		}

		if suffix.count > 0 {
			tasks = append(tasks, lineMatchTask{sourceStart: suffix.source, sourceEnd: suffix.source + suffix.count, peerStart: suffix.peer, peerEnd: suffix.peer + suffix.count, matched: true})
		}

		anchors := uniqueLineAnchors(ctx, source, peer, task)
		tasks = appendAnchorTasks(tasks, anchors, task)
	}

	return blocks
}

func trimLineMatchTask(ctx context.Context, source alignmentLines, peer alignmentLines, task lineMatchTask) (lineMatchTask, lineMatch, lineMatch) {
	startSource, startPeer := task.sourceStart, task.peerStart
	for task.sourceStart < task.sourceEnd && task.peerStart < task.peerEnd && linesSimilarity(source.at(task.sourceStart), peer.at(task.peerStart)) == EqualSimilarityLevel {
		if contextDone(ctx) {
			return task, lineMatch{}, lineMatch{}
		}

		task.sourceStart++
		task.peerStart++
	}

	endSource := task.sourceEnd
	for task.sourceStart < task.sourceEnd && task.peerStart < task.peerEnd && linesSimilarity(source.at(task.sourceEnd-1), peer.at(task.peerEnd-1)) == EqualSimilarityLevel {
		if contextDone(ctx) {
			return task, lineMatch{}, lineMatch{}
		}

		task.sourceEnd--
		task.peerEnd--
	}

	return task,
		lineMatch{source: startSource, peer: startPeer, count: task.sourceStart - startSource},
		lineMatch{source: task.sourceEnd, peer: task.peerEnd, count: endSource - task.sourceEnd}
}

func appendAnchorTasks(tasks []lineMatchTask, anchors []lineMatch, task lineMatchTask) []lineMatchTask {
	nextSource, nextPeer := task.sourceEnd, task.peerEnd

	for index := len(anchors) - 1; index >= 0; index-- {
		anchor := anchors[index]
		tasks = appendLineGap(tasks, anchor.source+1, nextSource, anchor.peer+1, nextPeer)
		tasks = append(tasks, lineMatchTask{sourceStart: anchor.source, sourceEnd: anchor.source + 1, peerStart: anchor.peer, peerEnd: anchor.peer + 1, matched: true})
		nextSource, nextPeer = anchor.source, anchor.peer
	}
	// No anchors means this entire middle is a changed gap.
	if len(anchors) != 0 {
		tasks = appendLineGap(tasks, task.sourceStart, nextSource, task.peerStart, nextPeer)
	}

	return tasks
}

func appendLineGap(tasks []lineMatchTask, sourceStart int, sourceEnd int, peerStart int, peerEnd int) []lineMatchTask {
	if sourceStart < sourceEnd && peerStart < peerEnd {
		tasks = append(tasks, lineMatchTask{sourceStart: sourceStart, sourceEnd: sourceEnd, peerStart: peerStart, peerEnd: peerEnd})
	}

	return tasks
}

func appendLineMatch(blocks []lineMatch, block lineMatch) []lineMatch {
	if len(blocks) != 0 {
		previous := &blocks[len(blocks)-1]
		if previous.source+previous.count == block.source && previous.peer+previous.count == block.peer {
			previous.count += block.count
			return blocks
		}
	}

	return append(blocks, block)
}

func (lines alignmentLines) withIndex(ctx context.Context) alignmentLines {
	if lines.index != nil {
		return lines
	}

	index := &fileMatchIndex{lines: map[string][]int{}, positions: lines.positions}
	for _, position := range lines.positions {
		if contextDone(ctx) {
			break
		}

		line := lines.file.lines[position]
		if !line.flagSet(matchesAlwaysDifferentLineFlag) {
			index.lines[line.text] = append(index.lines[line.text], position)
		}
	}

	lines.index = index

	return lines
}

func (lines alignmentLines) uniquePosition(text string, start int, end int) int {
	positions := lines.index.lines[text]
	first := sort.SearchInts(positions, lines.positions[start])

	boundary := lines.positions[end-1] + 1
	if first >= len(positions) || positions[first] >= boundary {
		return -1
	}

	if first+1 < len(positions) && positions[first+1] < boundary {
		return -1
	}

	return positions[first]
}

func uniqueLineAnchors(ctx context.Context, source alignmentLines, peer alignmentLines, task lineMatchTask) []lineMatch {
	if task.sourceStart == task.sourceEnd || task.peerStart == task.peerEnd {
		return nil
	}

	source, peer = source.withIndex(ctx), peer.withIndex(ctx)

	var candidates []lineMatch

	for index := task.sourceStart; index < task.sourceEnd; index++ {
		if contextDone(ctx) {
			return nil
		}

		text := source.at(index).text
		if source.uniquePosition(text, task.sourceStart, task.sourceEnd) != source.positions[index] {
			continue
		}

		if position := peer.uniquePosition(text, task.peerStart, task.peerEnd); position >= 0 {
			candidates = append(candidates, lineMatch{source: index, peer: sort.SearchInts(peer.positions, position), count: 1})
		}
	}

	return increasingLineAnchors(ctx, candidates)
}

func increasingLineAnchors(ctx context.Context, candidates []lineMatch) []lineMatch {
	// Longest increasing subsequence preserves both files' line order.
	tails := make([]int, 0, len(candidates))

	previous := make([]int, len(candidates))
	for index, candidate := range candidates {
		if contextDone(ctx) {
			return nil
		}

		length := sort.Search(len(tails), func(offset int) bool {
			return candidates[tails[offset]].peer >= candidate.peer
		})

		previous[index] = -1
		if length > 0 {
			previous[index] = tails[length-1]
		}

		if length == len(tails) {
			tails = append(tails, index)
		} else {
			tails[length] = index
		}
	}

	if len(tails) == 0 {
		return nil
	}

	anchors := make([]lineMatch, len(tails))
	for index, cursor := len(anchors)-1, tails[len(tails)-1]; index >= 0; index-- {
		anchors[index] = candidates[cursor]
		cursor = previous[cursor]
	}

	return anchors
}
