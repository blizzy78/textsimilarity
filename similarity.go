package textsimilarity

import (
	"context"
	"fmt"
	"io"
	"math/bits"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/blizzy78/textsimilarity/levenshtein"
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

	// SimilarSimilarityLevel is the similarity level used for lines or occurrences that are similar, but not completely equal.
	SimilarSimilarityLevel

	// EqualSimilarityLevel is the similarity level used for lines or occurrences that are completely equal.
	EqualSimilarityLevel
)

// DefaultMaxEditDistance is the Levenshtein distance used when Options.MaxEditDistance <= 0.
const DefaultMaxEditDistance = 5

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

	// MinSimilarLines is the minimum number of lines a similarity between files must have. Similarities with
	// fewer lines will not be reported.
	MinSimilarLines int

	// MaxEditDistance is the maximum Levenshtein distance between similar lines that will be considered "similar."
	// Lines that have a larger distance between them will be considered different.
	MaxEditDistance int

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
	// Occurrences is a set of text ranges in files.
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
	peers  []*fileToCheck
	inputs []*File
	blocks *blockSearch
}

// A fileLine is a single line of text in a file.
type fileLine struct {
	text                 string
	length               int
	flags                Flag
	bucket, lower, upper byte
	decoded              atomic.Pointer[lineText]
}

type lineText struct {
	once            sync.Once
	runes           []rune
	mask, low, high uint64
}

func (line *fileLine) textData() *lineText {
	data := line.decoded.Load()
	if data == nil {
		candidate := &lineText{}
		if line.decoded.CompareAndSwap(nil, candidate) {
			data = candidate
		} else {
			data = line.decoded.Load()
		}
	}

	data.once.Do(func() {
		data.runes = []rune(line.text)

		// Each bin counts runes modulo four. Gray coding makes an insertion or
		// deletion flip one bit, and a substitution at most two bits. Hash
		// collisions can only weaken this necessary edit-distance bound.
		var low, high uint64

		for _, char := range data.runes {
			bit := uint64(1) << uint32(char&63)
			data.mask |= bit
			high ^= low & bit
			low ^= bit
		}

		data.low, data.high = low^high, high
	})

	return data
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
func Similarities(ctx context.Context, files []*File, opts *Options) (<-chan *Similarity, <-chan Progress, error) { //nolint:gocognit,cyclop // it's complicated
	totalLines := 0

	width := blockWidth(opts)

	buffer := make([]byte, 32*1024)
	for _, f := range files {
		if err := f.loadBuffered(opts, width, buffer); err != nil {
			return nil, nil, err
		}

		totalLines += len(f.lines)
	}

	blocks := newBlockSearch(files, opts)
	filesToCheck := make([]*fileToCheck, len(files))
	for idx, file := range files {
		filesToCheck[idx] = &fileToCheck{f: file, inputs: files, blocks: blocks}
	}

	grp := sync.WaitGroup{}
	simsCh := make(chan *Similarity)
	progressCh := make(chan Progress)
	filesDone := int32(0)
	startTime := time.Now()
	semaphore := make(chan struct{}, runtime.NumCPU()+2)

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

	for _, file := range filesToCheck {
		grp.Add(1)

		go func(file *fileToCheck) {
			defer grp.Done()

			semaphore <- struct{}{}

			defer func() {
				<-semaphore
			}()

			if contextDone(ctx) {
				return
			}

			defer advanceAndSendProgress(file.f)

			sims := fileSimilarities(ctx, file, opts)
			for _, sim := range sims {
				simsCh <- sim
			}
		}(file)
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

		distinctSims := []*Similarity{}

	channel:
		for sim := range simsCh {
			sortOccurrences(sim.Occurrences)

			for _, dsim := range distinctSims {
				if equalSimilarities(sim, dsim) {
					continue channel
				}
			}

			distinctSims = append(distinctSims, sim)

			outCh <- sim
		}
	}()

	return outCh, progressCh, nil
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

		if !file.blocks.possible(ctx, file, fileLineIdx, opts) {
			continue
		}

		occurrences := []*FileOccurrence{}
		level := EqualSimilarityLevel
		tooShort := false

		peerCount := len(file.peers)
		if file.inputs != nil {
			peerCount = len(file.inputs)
		}

		for peerID := 0; peerID < peerCount; peerID++ {
			peerFile := file.peerAt(peerID)
			if contextDone(ctx) {
				return sims
			}

			startLine := 0
			if file.f == peerFile.f {
				startLine = fileLineIdx + 1
			}

			if opts.MinSimilarLines > 1 {
				first, _ := lineIndex(ctx, peerFile, line, startLine, opts)
				if first < 0 {
					continue
				}

				if !canReachMinimum(ctx, file, fileLineIdx, peerFile, first, opts) {
					tooShort = true
					break
				}

				startLine = first
			}

			peerFileOccurrences, peerFileLevel := lineOccurrences(ctx, peerFile, line, startLine, opts)
			if len(peerFileOccurrences) == 0 {
				continue
			}

			occurrences = append(occurrences, peerFileOccurrences...)

			if peerFileLevel < level {
				level = peerFileLevel
			}
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

		level = expandOccurrences(ctx, occurrences, level, opts)

		// The first occurrence is always retained by overlap filtering. Reject
		// short matches before building that filter, resetting every touched range.
		if occurrences[0].End-occurrences[0].Start < opts.MinSimilarLines {
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
	sourceEnd, peerEnd := sourceStart+1, peerStart+1
	for sourceEnd-sourceStart < opts.MinSimilarLines {
		if contextDone(ctx) {
			return false
		}

		left := nextAcceptedLine(ctx, source, sourceEnd, opts)

		right := nextAcceptedLine(ctx, peer, peerEnd, opts)
		if left < 0 || right < 0 || linesSimilarity(source.f.lines[left], peer.f.lines[right], opts) == differentSimilarityLevel {
			return false
		}

		sourceEnd, peerEnd = left+1, right+1
	}

	return true
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
// It also returns the similarity level of those occurrences.
func lineOccurrences(ctx context.Context, file *fileToCheck, line *fileLine, startLine int, opts *Options) ([]*FileOccurrence, SimilarityLevel) {
	occurrences := []*FileOccurrence{}
	level := EqualSimilarityLevel

	for {
		if contextDone(ctx) {
			return occurrences, level
		}

		fileLineIdx, fileLevel := lineIndex(ctx, file, line, startLine, opts)
		if fileLineIdx < 0 {
			return occurrences, level
		}

		occurrences = append(occurrences, &FileOccurrence{
			File:  file.f,
			Start: fileLineIdx,
			End:   fileLineIdx + 1,

			fileToCheck: file,
		})

		if fileLevel < level {
			level = fileLevel
		}

		startLine = fileLineIdx + 1
	}
}

// expandOccurrences expands occurrences in occs, that is, it will try to capture as much text as possible
// in each occurrence's file, according to opts. Each occurrence's End will be modified accordingly.
// The returned similarity level covering the modified occurrences may be lower than level (with respect to opts),
// but will never be similarityLevelDifferent.
func expandOccurrences(ctx context.Context, occs []*FileOccurrence, level SimilarityLevel, opts *Options) SimilarityLevel { //nolint:gocognit,cyclop // it's complicated
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
			return level
		}

		for idx, occ := range occs {
			next := nextAcceptedLine(ctx, occ.fileToCheck, ends[idx], opts)
			if next < 0 {
				return level
			}

			ends[idx] = next + 1
		}

		// check if files are still similar
		line1 := occs[0].fileToCheck.f.lines[ends[0]-1]

		for idx2, occ2 := range occs {
			if contextDone(ctx) {
				return level
			}

			if idx2 == 0 {
				continue
			}

			line2 := occ2.fileToCheck.f.lines[ends[idx2]-1]

			lineLevel := linesSimilarity(line1, line2, opts)
			if lineLevel == differentSimilarityLevel {
				return level
			}

			if lineLevel < level {
				level = lineLevel
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
	byFile := make(map[*File][]*FileOccurrence)

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

// lineIndexEnd returns the line index and similarity level of needle in file, starting with startLine,
// ending with endLine (excluding), according to opts. If no match can be found, -1 is returned for the line index.
func lineIndex(ctx context.Context, file *fileToCheck, needle *fileLine, startLine int, opts *Options) (int, SimilarityLevel) {
	endLine := len(file.f.lines)

	for lineIdx := startLine; ; lineIdx++ {
		if contextDone(ctx) {
			return -1, differentSimilarityLevel
		}

		if lineIdx >= endLine {
			return -1, differentSimilarityLevel
		}

		if file.isDone(lineIdx) {
			continue
		}

		level := linesSimilarity(file.f.lines[lineIdx], needle, opts)
		if level == differentSimilarityLevel {
			continue
		}

		return lineIdx, level
	}
}

// linesSimilarity returns the similarity level between fileLine1 and fileLine2, according to opts.
func linesSimilarity(fileLine1 *fileLine, fileLine2 *fileLine, opts *Options) SimilarityLevel {
	if (fileLine1.flags|fileLine2.flags)&matchesAlwaysDifferentLineFlag != 0 {
		return differentSimilarityLevel
	}

	if fileLine1.text == fileLine2.text {
		return EqualSimilarityLevel
	}

	return differentLinesSimilarity(fileLine1, fileLine2, opts)
}

// differentLinesSimilarity classifies lines whose text is not exactly equal.
func differentLinesSimilarity(fileLine1 *fileLine, fileLine2 *fileLine, opts *Options) SimilarityLevel {
	lenDiff := fileLine1.length - fileLine2.length
	if lenDiff < 0 {
		lenDiff = -lenDiff
	}

	maxDist := opts.MaxEditDistance
	if maxDist <= 0 {
		maxDist = DefaultMaxEditDistance
	}

	if lenDiff > maxDist {
		return differentSimilarityLevel
	}

	if max(fileLine1.length, fileLine2.length) <= maxDist {
		return SimilarSimilarityLevel
	}

	left, right := fileLine1.textData(), fileLine2.textData()

	changes := bits.OnesCount64(left.low^right.low) + bits.OnesCount64(left.high^right.high)
	if (changes+1)/2 > maxDist {
		return differentSimilarityLevel
	}

	// Each distinct rune absent from the other line requires at least one
	// edit. A substitution can fix one missing rune on each side, so test
	// the sides separately rather than adding their counts.
	if bits.OnesCount64(left.mask&^right.mask) > maxDist ||
		bits.OnesCount64(right.mask&^left.mask) > maxDist {
		return differentSimilarityLevel
	}

	if !levenshtein.WithinDistance(left.runes, right.runes, maxDist) {
		return differentSimilarityLevel
	}

	return SimilarSimilarityLevel
}

// load loads all lines from f, and sets up f accordingly, such as setting flags.
func (f *File) load(opts *Options) error { return f.loadBuffered(opts, 0, make([]byte, 32*1024)) }

func (f *File) loadBuffered(opts *Options, width int, buffer []byte) error {
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

	for idx := 0; len(text) > 0; idx++ {
		value, rest, newline := strings.Cut(text, "\n")
		text = rest

		if newline {
			value = strings.TrimSuffix(value, "\r")
		}

		line := &values[idx]
		line.fillHeader(value, opts)

		if width > 0 {
			// A radius of width/2 crosses at most one neighboring bucket.
			// Wrapping to eight bits can add candidates, never remove them.
			base, remainder := line.length/width, line.length%width
			limit := width / 2
			line.bucket = byte(base & 255)

			line.lower = line.bucket
			if remainder < limit {
				line.lower--
			}

			line.upper = line.bucket
			if remainder+limit >= width {
				line.upper++
			}
		}

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

func textToFileLine(text string, opts *Options) *fileLine {
	line := &fileLine{}
	line.fillHeader(text, opts)

	return line
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

// longEnough returns whether l is long enough to be considered for similarities at all, according to opts.
func (l *fileLine) longEnough(opts *Options) bool {
	return l.flagSet(blankLineFlag) || l.length >= opts.MinLineLength
}

// flagSet returns whether f is set in l.
func (l *fileLine) flagSet(f Flag) bool {
	return l.flags.set(f)
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
	if f.peers == nil {
		f.peers = make([]*fileToCheck, len(f.inputs))
	}

	if f.peers[index] == nil {
		f.peers[index] = &fileToCheck{f: f.inputs[index]}
	}

	return f.peers[index]
}
func (f *fileToCheck) peerDone(file int, line int) bool {
	return f.peers != nil && f.peers[file] != nil && f.peers[file].isDone(line)
}
