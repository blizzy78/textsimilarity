package textsimilarity

import (
	"context"
	"sort"
)

// Each accepted input position is indexed exactly once. Unlike fuzzy queries,
// exact lookups share the immutable postings directly, without cached copies.
type lineSearch struct {
	lines map[string]*lineQuery
}

type lineQuery struct {
	hits  []lineHit
	files []int
	short []lineHit
}

type lineHit struct {
	file int
	line int
}

func newLineSearch(files []*File, opts *Options) *lineSearch {
	search := &lineSearch{lines: map[string]*lineQuery{}}
	for fileID, file := range files {
		for lineID, line := range file.lines {
			if !acceptLine(line, opts) || line.flagSet(matchesAlwaysDifferentLineFlag) {
				continue
			}

			query := search.lines[line.text]
			if query == nil {
				query = &lineQuery{}
				search.lines[line.text] = query
			}

			query.add(lineHit{file: fileID, line: lineID}, len(file.lines)-lineID < opts.MinSimilarLines)
		}
	}

	return search
}

func (query *lineQuery) add(hit lineHit, short bool) {
	query.hits = append(query.hits, hit)
	if short {
		query.short = append(query.short, hit)
	}

	if len(query.files) == 0 || query.files[len(query.files)-1] != hit.file {
		query.files = append(query.files, hit.file)
	}
}

// Every occurrence in a reported group must meet minLines. A live seed too
// near EOF makes the full group unreportable, regardless of edit-run resets.
func (query *lineQuery) hasShortOccurrence(ctx context.Context, source *fileToCheck, start int) bool {
	for _, hit := range query.short {
		if contextDone(ctx) {
			return true
		}

		if hit.file == source.fileID && hit.line <= start {
			continue
		}

		if !source.peerDone(hit.file, hit.line) {
			return true
		}
	}

	return false
}

func (search *lineSearch) query(needle *fileLine) *lineQuery {
	if query := search.lines[needle.text]; query != nil {
		return query
	}

	return &lineQuery{}
}

func (search *lineSearch) find(ctx context.Context, file *fileToCheck, needle *fileLine, start int, opts *Options) (int, SimilarityLevel) {
	if !acceptLine(needle, opts) || needle.flagSet(matchesAlwaysDifferentLineFlag) {
		return -1, differentSimilarityLevel
	}

	hits := search.query(needle).hits

	begin := sort.Search(len(hits), func(index int) bool {
		return hits[index].file > file.fileID || hits[index].file == file.fileID && hits[index].line >= start
	})
	for _, hit := range hits[begin:] {
		if contextDone(ctx) || hit.file != file.fileID {
			break
		}

		if !file.isDone(hit.line) {
			return hit.line, EqualSimilarityLevel
		}
	}

	return -1, differentSimilarityLevel
}
