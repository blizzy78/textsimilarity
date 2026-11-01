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
