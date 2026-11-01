package textsimilarity

import (
	"context"
	"sync"
)

type tailSeed struct {
	line      *fileLine
	positions []uint64
}
type tailQuery struct {
	once      sync.Once
	positions []uint64
}

func tailSeeds(files []*File, count int) map[int][]tailSeed {
	groups := map[string]int{}

	var tails []tailSeed

	for fileID, file := range files {
		for lineIndex := max(0, len(file.lines)-count); lineIndex < len(file.lines); lineIndex++ {
			line := file.lines[lineIndex]

			group, found := groups[line.text]
			if !found {
				group = len(tails)
				groups[line.text] = group
				tails = append(tails, tailSeed{line: line})
			}

			tails[group].positions = append(tails[group].positions, uint64(fileID)<<32|uint64(lineIndex))
		}
	}

	seeds := map[int][]tailSeed{}
	for _, group := range tails {
		seeds[group.line.length] = append(seeds[group.line.length], group)
	}

	return seeds
}

func (search *lineSearch) matchesShortTail(ctx context.Context, source *fileToCheck, start int, opts *Options) bool {
	if search.shortTails == nil {
		return false
	}

	limit := opts.MaxEditDistance
	if limit <= 0 {
		limit = DefaultMaxEditDistance
	}
	// An occurrence shorter than minimum minus the line budget cannot align
	// enough source lines, even by spending the entire budget on deletions.
	return search.shortTails.matchesTail(ctx, source, start, limit, opts)
}

func (index *blockSearch) matchesTail(ctx context.Context, source *fileToCheck, start int, limit int, opts *Options) bool {
	if index.tails == nil || source.search == nil {
		return false
	}

	needle := source.f.lines[start]
	if !source.search.hasIndex(needle) {
		return false
	}

	prepared := source.search.prepared(needle)
	if len(prepared.positions) < 2 && needle.length > 2*limit+5 {
		return false
	}

	positions := index.tailPositions(ctx, source.search, needle, limit, opts)
	for _, position := range positions {
		file, line := int(position>>32), int(position&0xffffffff)
		if index.files[file] == source.f && line <= start || source.peerDone(file, line) {
			continue
		}

		return true
	}

	return false
}

func (index *blockSearch) tailPositions(ctx context.Context, search *lineSearch, needle *fileLine, limit int, opts *Options) []uint64 { //nolint:gocognit // cache a bounded set of real short suffixes once per seed
	value, found := index.tailQueries.Load(needle.text)
	if !found {
		value, _ = index.tailQueries.LoadOrStore(needle.text, &tailQuery{})
	}

	query, _ := value.(*tailQuery)
	query.once.Do(func() {
		for length := max(0, needle.length-limit); length <= needle.length+limit; length++ {
			for _, group := range index.tails[length] {
				if contextDone(ctx) {
					return
				}

				if search.compare(needle, group.line, opts) == differentSimilarityLevel {
					continue
				}

				query.positions = append(query.positions, group.positions[:min(8-len(query.positions), len(group.positions))]...)
				if len(query.positions) == 8 {
					return
				}
			}
		}
	})

	return query.positions
}
