package textsimilarity

import "sync/atomic"

func newAnchorSearch(files []*File, opts *Options) *blockSearch {
	if blockWidth(opts) == 0 {
		return nil
	}

	count := 0
	for _, file := range files {
		count += max(0, len(file.lines)-opts.MinSimilarLines+1)
		if count > 2000000 {
			return nil
		}
	}

	index := &blockSearch{files: files, span: opts.MinSimilarLines, dead: make([][]atomic.Uint32, len(files))}
	index.tree = newWindowTree(files, opts.MinSimilarLines)
	groups := map[string]int{}
	tailGroups := map[string]int{}

	var tails []tailSeed

	for idx, file := range files {
		index.dead[idx] = make([]atomic.Uint32, len(file.lines))
		if len(file.lines) == 0 {
			continue
		}

		last := len(file.lines) - 1
		line := file.lines[last]

		group, found := groups[line.text]
		if !found {
			group = len(index.ends)
			groups[line.text] = group
			index.ends = append(index.ends, terminalGroup{line: line})
			index.maxEnd = max(index.maxEnd, line.length)
		}

		index.ends[group].locations = append(index.ends[group].locations, blockLocation{file: uint32(idx), line: uint32(last)}) //nolint:gosec // total windows are bounded to two million above
		for lineIndex := max(0, len(file.lines)-opts.MinSimilarLines+1); lineIndex < len(file.lines); lineIndex++ {
			line := file.lines[lineIndex]

			group, found := tailGroups[line.text]
			if !found {
				group = len(tails)
				tailGroups[line.text] = group
				tails = append(tails, tailSeed{line: line})
			}

			tails[group].positions = append(tails[group].positions, uint64(idx)<<32|uint64(lineIndex))
		}
	}

	index.tails = map[int][]tailSeed{}
	for _, group := range tails {
		index.tails[group.line.length] = append(index.tails[group.line.length], group)
	}

	return index
}
