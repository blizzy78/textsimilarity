package textsimilarity

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/dropbox/godropbox/container/bitvector"
)

const (
	// IgnoreWhitespaceFlag specifies that leading and trailing whitespace of text lines should be ignored.
	IgnoreWhitespaceFlag = Flag(1 << iota)

	// IgnoreBlankLinesFlag specifies that blank lines should be ignored.
	IgnoreBlankLinesFlag
)

const (
	// differentSimilarityLevel is the similarity level used for lines that are completely different.
	differentSimilarityLevel = SimilarityLevel(iota) // not exported

	// SimilarSimilarityLevel identifies occurrences that contain line edits.
	SimilarSimilarityLevel

	// EqualSimilarityLevel identifies lines or occurrences equal under the configured ignore rules.
	EqualSimilarityLevel
)

// DefaultMaxLineDistance is the CLI's default consecutive line-edit limit.
const DefaultMaxLineDistance = 3

const (
	// blankLineFlag is set on a fileLine when that line is blank.
	blankLineFlag = Flag(1 << iota)

	// matchesIgnoreRegexLineFlag is set on a fileLine when that line's text matches Options.IgnoreLineRegex.
	matchesIgnoreRegexLineFlag

	// matchesAlwaysDifferentLineFlag is set on a fileLine when that line's text matches Options.AlwaysDifferentLineRegex.
	matchesAlwaysDifferentLineFlag
)

// Options specifies several options for determining similarities.
type Options struct {
	// Flags is a set of flags specifying different behaviour in determining similarities, such as ignoring whitespace or blank lines.
	Flags Flag

	// MinLineLength is the minimum length of a line to be considered (in runes.) Lines shorter than that will be ignored.
	MinLineLength int

	// MinSimilarLines is the minimum number of non-ignored lines in every occurrence
	// of a reported group. Ignored lines do not contribute to this minimum.
	MinSimilarLines int

	// MaxLineDistance limits consecutive line insertions, deletions, and replacements
	// relative to the source occurrence. Every unequal line costs one edit. An exact
	// match resets the counter; ignored lines neither cost an edit nor reset it.
	// Each peer has its own counter. Nonpositive values require exact matches.
	// Positive limits bridge edit gaps between unambiguous exact-line anchors;
	// an oversized ambiguous gap ends expansion before that gap.
	MaxLineDistance int

	// IgnoreLineRegex, if set, is an expression that a line must match to be ignored. Note that leading/trailing
	// whitespace on lines as well as blank lines may be ignored by using Flags.
	IgnoreLineRegex *regexp.Regexp

	// AlwaysDifferentLineRegex, if set, is an expression that a line must match to be always considered different.
	AlwaysDifferentLineRegex *regexp.Regexp
}

// A Flag is a single flag (a single set bit), or a set of flags (multiple set bits), depending on the context.
type Flag uint8

// A File is a source of text lines read from a Reader.
type File struct {
	// Name is an arbitrary name for the file.
	Name string

	// R is read from to get the file's contents. The contents is expected to be UTF-8 text.
	R io.Reader

	// lines is a slice of line texts (indexed by zero-based line numbers).
	lines []*fileLine
}

// A Similarity is a match of ranges of text between different Files.
type Similarity struct {
	// Occurrences is a set of text ranges in files. The first is the reference used
	// to validate every other occurrence. The remaining occurrences are sorted by
	// file name, start and end. Other peer pairs need not satisfy the edit limit.
	Occurrences []*FileOccurrence

	// Level is the level of similarity between Occurrences.
	Level SimilarityLevel
}

// A FileOccurrence is a range of text within a single File.
type FileOccurrence struct {
	// File is the file the range of text was found in.
	File *File

	// Start is the starting line number (zero-based.)
	Start int

	// End is the ending line number (zero-based, exclusive.)
	End int

	fileToCheck *fileToCheck
}

// SimilarityLevel is the level of similarity between ranges of text.
type SimilarityLevel int

// Progress is reported when determining similarities.
type Progress struct {
	// File is the file that has just been processed.
	File *File

	// Done is an overall progress percentage value from 0 to 1.
	Done float64

	// ETA is an estimate of the time of completion.
	ETA time.Time
}

// A fileToCheck is a file that needs to be processed, along with its peers.
type fileToCheck struct {
	// f is the File that should be processed.
	f *File

	// linesDone is a bit vector representing the file's lines. When a line has been processed or if it ends up
	// as part of a similarity, its bit in the vector will be set. In that case, the line can be skipped while
	// iterating.
	linesDone *bitVector

	// peers are all the files this file needs to be checked against, including itself.
	peers     []*fileToCheck
	peerCache map[int]*fileToCheck
	inputs    []*File
	search    *lineSearch
	fileID    int
}

// A fileLine is a single line of text in a file.
type fileLine struct {
	text   string
	length int
	flags  Flag
}

// A bitVector is a compact set of bits.
type bitVector bitvector.BitVector

// intSlicePool is used to allocate []int, and to help with garbage collection.
var intSlicePool = sync.Pool{
	New: func() any {
		// 1024 should be a reasonably high number of occurrences for a similarity,
		// higher numbers will be satisfied from outside of the pool
		return make([]int, 0, 1024)
	},
}

// Similarities scans files for similarities between them, according to opts. Detected similarities
// will be sent into the returned channel. Progress is reported via the returned progress channel.
// Both channels must be drained by the caller.
func Similarities(ctx context.Context, files []*File, opts *Options) (<-chan *Similarity, <-chan Progress, error) {
	buffer := make([]byte, 32*1024)
	for _, f := range files {
		if err := f.loadBuffered(opts, buffer); err != nil {
			return nil, nil, err
		}
	}

	sims, progress := similaritiesLoaded(ctx, files, opts)

	return sims, progress, nil
}

func similaritiesLoaded(ctx context.Context, files []*File, opts *Options) (<-chan *Similarity, <-chan Progress) { //nolint:gocognit,cyclop // concurrent scanning, deduplication, progress, and result delivery
	search := newLineSearch(files, opts)

	filesToCheck := make([]*fileToCheck, len(files))
	for idx, file := range files {
		filesToCheck[idx] = &fileToCheck{f: file, inputs: files, search: search, fileID: idx}
	}

	grp := sync.WaitGroup{}
	simsCh := make(chan *Similarity, 64)
	progressCh := make(chan Progress)
	filesDone := int32(0)
	startTime := time.Now()

	advanceAndSendProgress := func(file *File) {
		if contextDone(ctx) {
			return
		}

		flDone := int(atomic.AddInt32(&filesDone, 1))

		elapsed := time.Since(startTime)
		total := time.Duration(int64(float64(elapsed) * float64(len(files)) / float64(flDone)))
		remaining := total - elapsed

		progressCh <- Progress{
			File: file,
			Done: float64(flDone) * 100.0 / float64(len(files)),
			ETA:  time.Now().Add(remaining),
		}
	}

	slices.SortFunc(filesToCheck, func(left *fileToCheck, right *fileToCheck) int {
		return len(right.f.lines) - len(left.f.lines)
	})

	var next atomic.Uint32

	for worker := 0; worker < min(len(filesToCheck), 2*runtime.GOMAXPROCS(0)); worker++ {
		grp.Go(func() {
			for {
				idx := int(next.Add(1)) - 1
				if idx >= len(filesToCheck) || contextDone(ctx) {
					return
				}

				file := filesToCheck[idx]
				sims := fileSimilarities(ctx, file, opts)
				advanceAndSendProgress(file.f)

				for _, sim := range sims {
					simsCh <- sim
				}
			}
		})
	}

	go func() {
		defer close(simsCh)
		defer close(progressCh)

		grp.Wait()
	}()

	outCh := make(chan *Similarity)

	go func() {
		defer close(outCh)

		// help GC
		defer func() {
			for _, f := range files {
				f.lines = nil
			}
		}()

		distinctSims := newSimilaritySet(files)

		for sim := range simsCh {
			sortOccurrences(sim.Occurrences[1:])

			if !distinctSims.add(sim) {
				continue
			}

			outCh <- sim
		}
	}()

	return outCh, progressCh
}

// fileSimilarities returns all similarities between file and its peers, according to opts.
func fileSimilarities(ctx context.Context, file *fileToCheck, opts *Options) []*Similarity { //nolint:gocognit,cyclop // it's complicated
	sims := []*Similarity{}

	for fileLineIdx := 0; ; fileLineIdx++ {
		if contextDone(ctx) {
			return sims
		}

		if fileLineIdx >= len(file.f.lines) {
			break
		}

		if file.isDone(fileLineIdx) {
			continue
		}

		line := file.f.lines[fileLineIdx]
		if !acceptLine(line, opts) {
			continue
		}

		if len(file.f.lines)-fileLineIdx < opts.MinSimilarLines {
			continue
		}

		occurrences := []*FileOccurrence{}
		tooShort := false

		peerCount := len(file.peers)
		if file.inputs != nil {
			peerCount = len(file.inputs)
		}

		var peerIDs []int

		if file.search != nil {
			query := file.search.query(line)
			if query.hasShortOccurrence(ctx, file, fileLineIdx) {
				continue
			}

			peerIDs = query.files
			peerCount = len(peerIDs)
		}

		for peerIndex := 0; peerIndex < peerCount; peerIndex++ {
			peerID := peerIndex
			if peerIDs != nil {
				peerID = peerIDs[peerIndex]
			}

			peerFile := file.peerAt(peerID)

			if contextDone(ctx) {
				return sims
			}

			startLine := 0
			if file.f == peerFile.f {
				startLine = fileLineIdx + 1
			}

			if opts.MinSimilarLines > 1 {
				first := lineIndex(ctx, peerFile, line, startLine, opts)
				if first < 0 {
					continue
				}

				if len(peerFile.f.lines)-first < opts.MinSimilarLines || !canReachMinimum(ctx, file, fileLineIdx, peerFile, first, opts) {
					tooShort = true
					break
				}

				startLine = first
			}

			peerFileOccurrences := lineOccurrences(ctx, peerFile, line, startLine, opts)
			if len(peerFileOccurrences) == 0 {
				continue
			}

			occurrences = append(occurrences, peerFileOccurrences...)
		}

		if tooShort || len(occurrences) == 0 {
			continue
		}

		occurrences = append([]*FileOccurrence{
			{
				File:  file.f,
				Start: fileLineIdx,
				End:   fileLineIdx + 1,

				fileToCheck: file,
			},
		}, occurrences...)

		level := expandOccurrences(ctx, occurrences, opts)

		// Reject groups with too few accepted lines in any occurrence. Ignored
		// lines inside a large physical span must not inflate the minimum.
		for _, occ := range occurrences {
			tooShort = tooShort || !occurrenceLongEnough(ctx, occ, opts)
		}

		if tooShort {
			// reset lines done
			for _, occ := range occurrences {
				for l := occ.Start; l < occ.End; l++ {
					occ.fileToCheck.setDone(l, false)
				}
			}

			continue
		}

		occurrences = filterSameFileOverlappingOccurrences(occurrences)
		if len(occurrences) < 2 {
			// reset lines done
			for _, occ := range occurrences {
				for l := occ.Start; l < occ.End; l++ {
					occ.fileToCheck.setDone(l, false)
				}
			}

			continue
		}

		sims = append(sims, &Similarity{
			Occurrences: occurrences,
			Level:       level,
		})

		markOccurrencesLinesDone(occurrences)

		// skip all lines in file that appear in occurrences that refer to file.f -
		// in other words, occurrences in file below the current line
		for _, occ := range occurrences[1:] {
			if occ.fileToCheck.f != file.f {
				continue
			}

			for l := occ.Start; l < occ.End; l++ {
				file.setDone(l, true)
			}
		}

		// subtract 1 because of loop's increment
		fileLineIdx = occurrences[0].End - 1
	}

	return sims
}

// canReachMinimum checks a necessary condition for expanding the full set of
// occurrences. It never marks lines: other occurrences can only shorten the
// eventual expansion by marking additional lines done, not extend it.
func canReachMinimum(ctx context.Context, source *fileToCheck, sourceStart int, peer *fileToCheck, peerStart int, opts *Options) bool {
	if opts.MaxLineDistance > 0 {
		// Canonical anchors depend on complete tails. This probe allows every
		// possible alignment, so its negative result is safe for anchored ones.
		return hasMinimumAccepted(ctx, source, sourceStart, opts) && hasMinimumAccepted(ctx, peer, peerStart, opts) && canReachLinePrefix(ctx, source, sourceStart, peer, peerStart, opts)
	}

	sourceEnd, peerEnd := sourceStart+1, peerStart+1

	for matched := 1; matched < opts.MinSimilarLines; matched++ {
		if contextDone(ctx) {
			return false
		}

		left := nextAcceptedLine(ctx, source, sourceEnd, opts)

		right := nextAcceptedLine(ctx, peer, peerEnd, opts)
		if left < 0 || right < 0 || linesSimilarity(source.f.lines[left], peer.f.lines[right]) == differentSimilarityLevel {
			return false
		}

		sourceEnd, peerEnd = left+1, right+1
	}

	return true
}

func hasMinimumAccepted(ctx context.Context, file *fileToCheck, start int, opts *Options) bool {
	for count := 0; count < opts.MinSimilarLines; count++ {
		line := nextAcceptedLine(ctx, file, start, opts)
		if line < 0 {
			return false
		}

		start = line + 1
	}

	return !contextDone(ctx)
}

func occurrenceLongEnough(ctx context.Context, occurrence *FileOccurrence, opts *Options) bool {
	count := 0
	for line := occurrence.Start; line < occurrence.End && count < opts.MinSimilarLines; line++ {
		if contextDone(ctx) {
			return false
		}

		if acceptLine(occurrence.fileToCheck.f.lines[line], opts) {
			count++
		}
	}

	return count >= opts.MinSimilarLines
}

func nextAcceptedLine(ctx context.Context, file *fileToCheck, start int, opts *Options) int {
	for idx := start; idx < len(file.f.lines); idx++ {
		if contextDone(ctx) || file.isDone(idx) {
			return -1
		}

		if acceptLine(file.f.lines[idx], opts) {
			return idx
		}
	}

	return -1
}

// markOccurrencesLinesDone marks all lines as done that are referred to by occs.
func markOccurrencesLinesDone(occs []*FileOccurrence) {
	for _, occ := range occs {
		for l := occ.Start; l < occ.End; l++ {
			occ.fileToCheck.setDone(l, true)
		}
	}
}

// lineOccurrences returns all occurrences of line in file, beginning with startLine, according to opts.
func lineOccurrences(ctx context.Context, file *fileToCheck, line *fileLine, startLine int, opts *Options) []*FileOccurrence {
	occurrences := []*FileOccurrence{}

	for {
		if contextDone(ctx) {
			return occurrences
		}

		fileLineIdx := lineIndex(ctx, file, line, startLine, opts)
		if fileLineIdx < 0 {
			return occurrences
		}

		occurrences = append(occurrences, &FileOccurrence{
			File:  file.f,
			Start: fileLineIdx,
			End:   fileLineIdx + 1,

			fileToCheck: file,
		})

		startLine = fileLineIdx + 1
	}
}

// expandOccurrences expands occurrences in occs, that is, it will try to capture as much text as possible
// in each occurrence's file, according to opts. Each occurrence's End will be modified accordingly.
// The result is similar when expansion includes edits, otherwise equal.
func expandOccurrences(ctx context.Context, occs []*FileOccurrence, opts *Options) SimilarityLevel { //nolint:gocognit,cyclop // it's complicated
	if opts.MaxLineDistance > 0 {
		return expandLineOccurrences(ctx, occs, opts)
	}

	ends := intSlicePool.Get().([]int) //nolint:forcetypeassert // we know what's in the pool
	ends = ends[:0]

	if cap(ends) >= len(occs) {
		defer intSlicePool.Put(ends) //nolint:staticcheck // slice is pointer-like
	} else {
		intSlicePool.Put(ends) //nolint:staticcheck // slice is pointer-like

		ends = make([]int, 0, len(occs))
	}

	ends = ends[:len(occs)]
	for idx, occ := range occs {
		ends[idx] = occ.End
	}

	expanded := false

	for {
		if contextDone(ctx) {
			return EqualSimilarityLevel
		}

		for idx, occ := range occs {
			next := nextAcceptedLine(ctx, occ.fileToCheck, ends[idx], opts)
			if next < 0 {
				return EqualSimilarityLevel
			}

			ends[idx] = next + 1
		}

		// check if files are still similar
		line1 := occs[0].fileToCheck.f.lines[ends[0]-1]

		for idx2, occ2 := range occs {
			if contextDone(ctx) {
				return EqualSimilarityLevel
			}

			if idx2 == 0 {
				continue
			}

			line2 := occ2.fileToCheck.f.lines[ends[idx2]-1]

			lineLevel := linesSimilarity(line1, line2)
			if lineLevel == differentSimilarityLevel {
				return EqualSimilarityLevel
			}
		}

		// commit new ends
		for idx, occ := range occs {
			start := occ.End
			if !expanded {
				start = occ.Start
			}

			occ.End = ends[idx]

			// Earlier iterations already marked the existing range. Only mark
			// newly included lines, including any ignored lines we skipped.
			for l := start; l < occ.End; l++ {
				occ.fileToCheck.setDone(l, true)
			}
		}

		expanded = true
	}
}

// filterSameFileOverlappingOccurrences filters out occurrences that overlap with each other in the same file.
func filterSameFileOverlappingOccurrences(occurrences []*FileOccurrence) []*FileOccurrence {
	nonOverlapping := make([]*FileOccurrence, 0, len(occurrences))
	byFile := map[*File][]*FileOccurrence{}

	for _, occ := range occurrences {
		accepted := byFile[occ.fileToCheck.f]

		position := len(accepted)
		if position > 0 && occ.Start < accepted[position-1].End {
			position = sort.Search(len(accepted), func(idx int) bool {
				return accepted[idx].Start >= occ.Start
			})
		}

		overlap := position > 0 && accepted[position-1].End > occ.Start ||
			position < len(accepted) && accepted[position].Start < occ.End

		if overlap {
			// reset lines done
			for l := occ.Start; l < occ.End; l++ {
				occ.fileToCheck.setDone(l, false)
			}

			continue
		}

		accepted = append(accepted, nil)
		copy(accepted[position+1:], accepted[position:len(accepted)-1])
		accepted[position] = occ
		byFile[occ.fileToCheck.f] = accepted

		nonOverlapping = append(nonOverlapping, occ)
	}

	return nonOverlapping
}

// acceptLine returns whether line should be considered for similarities at all, according to opts.
func acceptLine(line *fileLine, opts *Options) bool {
	if opts.flagSet(IgnoreBlankLinesFlag) && line.flagSet(blankLineFlag) {
		return false
	}

	if !line.longEnough(opts) {
		return false
	}

	if line.flagSet(matchesIgnoreRegexLineFlag) {
		return false
	}

	return true
}

// lineIndex returns the index of an exact seed match at or after startLine,
// according to opts. If no match can be found, -1 is returned for the line index.
func lineIndex(ctx context.Context, file *fileToCheck, needle *fileLine, startLine int, opts *Options) int {
	if file.search != nil {
		return file.search.find(ctx, file, needle, startLine, opts)
	}

	return linearLineIndex(ctx, file, needle, startLine, opts)
}

func linearLineIndex(ctx context.Context, file *fileToCheck, needle *fileLine, startLine int, opts *Options) int {
	endLine := len(file.f.lines)

	for lineIdx := startLine; ; lineIdx++ {
		if contextDone(ctx) {
			return -1
		}

		if lineIdx >= endLine {
			return -1
		}

		if file.isDone(lineIdx) {
			continue
		}

		if !acceptLine(file.f.lines[lineIdx], opts) {
			continue
		}

		level := linesSimilarity(file.f.lines[lineIdx], needle)
		if level == differentSimilarityLevel {
			continue
		}

		return lineIdx
	}
}

// linesSimilarity reports exact equality unless either line is forced different.
func linesSimilarity(fileLine1 *fileLine, fileLine2 *fileLine) SimilarityLevel {
	if (fileLine1.flags|fileLine2.flags)&matchesAlwaysDifferentLineFlag != 0 {
		return differentSimilarityLevel
	}

	if fileLine1.text == fileLine2.text {
		return EqualSimilarityLevel
	}

	return differentSimilarityLevel
}

// loadBuffered loads all lines from f and prepares their headers and flags.
func (f *File) loadBuffered(opts *Options, buffer []byte) error {
	f.lines = nil

	var builder strings.Builder

	_, err := io.CopyBuffer(&builder, &progressReader{reader: f.R}, buffer)
	if err != nil {
		return fmt.Errorf("read line: read line: %w", err)
	}

	text := builder.String()
	if text == "" {
		return nil
	}

	count := strings.Count(text, "\n")
	if text[len(text)-1] != 10 {
		count++
	}

	values := make([]fileLine, count)
	f.lines = make([]*fileLine, count)

	for idx := 0; len(text) != 0; idx++ {
		value, rest, newline := strings.Cut(text, "\n")
		text = rest

		if newline {
			value = strings.TrimSuffix(value, "\r")
		}

		line := &values[idx]
		line.fillHeader(value, opts)

		f.lines[idx] = line
	}

	return nil
}

type progressReader struct {
	reader io.Reader
	empty  int
}

func (r *progressReader) Read(data []byte) (int, error) {
	read, err := r.reader.Read(data)
	if read == 0 && err == nil {
		r.empty++
		if r.empty >= 100 {
			return 0, io.ErrNoProgress
		}
	} else {
		r.empty = 0
	}

	return read, err //nolint:wrapcheck // preserve the underlying io.Reader error
}

func (line *fileLine) fillHeader(text string, opts *Options) {
	if opts.flagSet(IgnoreWhitespaceFlag) {
		text = strings.TrimSpace(text)
	}

	line.text = text

	line.length = utf8.RuneCountInString(text)
	if line.length == 0 {
		line.flags |= blankLineFlag
	}

	if opts.IgnoreLineRegex != nil && opts.IgnoreLineRegex.MatchString(text) {
		line.flags |= matchesIgnoreRegexLineFlag
	}

	if opts.AlwaysDifferentLineRegex != nil && opts.AlwaysDifferentLineRegex.MatchString(text) {
		line.flags |= matchesAlwaysDifferentLineFlag
	}
}

// flagSet returns whether f is set in o.
func (o Options) flagSet(f Flag) bool {
	return o.Flags.set(f)
}

// newBitVector returns a new empty bit vector of length.
func newBitVector(length int) *bitVector {
	bytes := length / 8
	if bytes*8 < length {
		bytes++
	}

	data := make([]byte, bytes)

	return (*bitVector)(bitvector.NewBitVector(data, length))
}

// isSet returns whether bit idx is set in b.
func (b *bitVector) isSet(idx int) bool {
	return (*bitvector.BitVector)(b).Element(idx) == 1
}

// set sets bit idx in b to v.
func (b *bitVector) set(idx int, v bool) {
	val := byte(0)
	if v {
		val = 1
	}

	(*bitvector.BitVector)(b).Set(val, idx)
}

// longEnough returns whether line is long enough to be considered for similarities at all, according to opts.
func (line *fileLine) longEnough(opts *Options) bool {
	return line.flagSet(blankLineFlag) || line.length >= opts.MinLineLength
}

// flagSet returns whether f is set in line.
func (line *fileLine) flagSet(f Flag) bool {
	return line.flags.set(f)
}

// set sets flag in f.
func (f Flag) set(flag Flag) bool {
	return f&flag != 0
}

// equalSimilarities returns whether sim1 and sim2 are equal.
func equalSimilarities(sim1 *Similarity, sim2 *Similarity) bool {
	if len(sim1.Occurrences) != len(sim2.Occurrences) {
		return false
	}

	occs1 := make([]*FileOccurrence, len(sim1.Occurrences))
	copy(occs1, sim1.Occurrences)
	sortOccurrences(occs1)

	occs2 := make([]*FileOccurrence, len(sim2.Occurrences))
	copy(occs2, sim2.Occurrences)
	sortOccurrences(occs2)

	for i := range occs1 {
		if !equalOccurrences(occs1[i], occs2[i]) {
			return false
		}
	}

	return true
}

// equalOccurrences returns whether occ1 and occ2 are equal.
func equalOccurrences(occ1 *FileOccurrence, occ2 *FileOccurrence) bool {
	return occ1.File == occ2.File && occ1.Start == occ2.Start && occ1.End == occ2.End
}

// sortOccurrences sorts occs by their File.Name, then by their Start, and then by their End.
func sortOccurrences(occs []*FileOccurrence) {
	sort.SliceStable(occs, func(a int, b int) bool {
		occ1 := occs[a]
		occ2 := occs[b]

		switch {
		case occ1.File.Name < occ2.File.Name:
			return true
		case occ1.File.Name > occ2.File.Name:
			return false
		}

		switch {
		case occ1.Start < occ2.Start:
			return true
		case occ1.Start > occ2.Start:
			return false
		}

		return occ1.End < occ2.End
	})
}

// contextDone returns whether ctx is done.
func contextDone(ctx context.Context) bool {
	return ctx.Err() != nil
}

func (f *fileToCheck) isDone(line int) bool { return f.linesDone != nil && f.linesDone.isSet(line) }

func (f *fileToCheck) setDone(line int, done bool) {
	if f.linesDone == nil {
		if !done {
			return
		}

		f.linesDone = newBitVector(len(f.f.lines))
	}

	f.linesDone.set(line, done)
}
func (f *fileToCheck) peerAt(index int) *fileToCheck {
	if f.inputs != nil {
		if f.peerCache == nil {
			f.peerCache = map[int]*fileToCheck{}
		}

		peer := f.peerCache[index]
		if peer == nil {
			peer = &fileToCheck{f: f.inputs[index], search: f.search, fileID: index}
			f.peerCache[index] = peer
		}

		return peer
	}

	return f.peers[index]
}
func (f *fileToCheck) peerDone(file int, line int) bool {
	if f.peerCache != nil {
		peer := f.peerCache[file]
		return peer != nil && peer.isDone(line)
	}

	return f.peers != nil && f.peers[file] != nil && f.peers[file].isDone(line)
}
