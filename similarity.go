package textsimilarity

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	slowlevenshtein "github.com/agext/levenshtein"
	tsio "github.com/blizzy78/textsimilarity/internal/io"
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

	// slowLevenshteinLineFlag is set on a fileLine when that line's text must be used with the "slow"
	// Levenshtein distance calculation.
	slowLevenshteinLineFlag

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

	// lines is a map of line numbers (zero-based) to line text.
	lines map[int]*fileLine
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
	peers []*fileToCheck
}

// A fileLine is a single line of text in a file.
type fileLine struct {
	// text is the original line of text.
	text string

	// textTrimmed is the line of text sans leading and trailing whitespace.
	textTrimmed string

	// textRunes is the original line of text.
	textRunes []rune

	// textTrimmedRunes is the line of text sans leading and trailing whitespace.
	textTrimmedRunes []rune

	// length is the length of text (in runes.)
	length int

	// lengthTrimmed is the length of textTrimmed (in runes.)
	lengthTrimmed int

	// flags is a set of line flags, such as whether this line is blank.
	flags Flag
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

	for _, f := range files {
		if err := f.load(opts); err != nil {
			return nil, nil, err
		}

		totalLines += len(f.lines)
	}

	filesToCheck := make([]*fileToCheck, len(files))

	for idx, file := range files {
		ftc := fileToCheck{
			f:         file,
			linesDone: newBitVector(len(file.lines)),
		}

		for _, peerFile := range files {
			peer := fileToCheck{
				f:         peerFile,
				linesDone: newBitVector(len(peerFile.lines)),
			}

			ftc.peers = append(ftc.peers, &peer)
		}

		filesToCheck[idx] = &ftc
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

		if file.linesDone.isSet(fileLineIdx) {
			continue
		}

		line := file.f.lines[fileLineIdx]
		if !acceptLine(line, opts) {
			continue
		}

		occurrences := []*FileOccurrence{}
		level := EqualSimilarityLevel

		for _, peerFile := range file.peers {
			if contextDone(ctx) {
				return sims
			}

			startLine := 0
			if file.f == peerFile.f {
				startLine = fileLineIdx + 1
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

		if len(occurrences) == 0 {
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

		occurrences = filterSameFileOverlappingOccurrences(occurrences)
		if len(occurrences) < 2 {
			// reset lines done
			for _, occ := range occurrences {
				for l := occ.Start; l < occ.End; l++ {
					occ.fileToCheck.linesDone.set(l, false)
				}
			}

			continue
		}

		if occurrences[0].End-occurrences[0].Start < opts.MinSimilarLines {
			// reset lines done
			for _, occ := range occurrences {
				for l := occ.Start; l < occ.End; l++ {
					occ.fileToCheck.linesDone.set(l, false)
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
				file.linesDone.set(l, true)
			}
		}

		// subtract 1 because of loop's increment
		fileLineIdx = occurrences[0].End - 1
	}

	return sims
}

// markOccurrencesLinesDone marks all lines as done that are referred to by occs.
func markOccurrencesLinesDone(occs []*FileOccurrence) {
	for _, occ := range occs {
		for l := occ.Start; l < occ.End; l++ {
			occ.fileToCheck.linesDone.set(l, true)
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

	for {
		if contextDone(ctx) {
			return level
		}

		for _, occ := range occs {
			// this will never create a new backing array because of capacity checks above
			ends = append(ends, occ.End)
		}

		for idx, occ := range occs {
			for {
				if contextDone(ctx) {
					return level
				}

				ends[idx]++

				if ends[idx] > len(occ.fileToCheck.f.lines) {
					return level
				}

				if occ.fileToCheck.linesDone.isSet(ends[idx] - 1) {
					return level
				}

				line := occ.fileToCheck.f.lines[ends[idx]-1]
				if acceptLine(line, opts) {
					break
				}
			}
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
		for i, occ := range occs {
			occ.End = ends[i]

			// mark lines done
			for l := occ.Start; l < occ.End; l++ {
				occ.fileToCheck.linesDone.set(l, true)
			}
		}
	}
}

// filterSameFileOverlappingOccurrences filters out occurrences that overlap with each other in the same file.
func filterSameFileOverlappingOccurrences(occurrences []*FileOccurrence) []*FileOccurrence {
	nonOverlapping := make([]*FileOccurrence, 0, len(occurrences))

	for _, occ := range occurrences {
		overlap := false

		for _, nonOverlapOcc := range nonOverlapping {
			if nonOverlapOcc.fileToCheck.f != occ.fileToCheck.f {
				continue
			}

			if nonOverlapOcc.Start < occ.End && occ.Start < nonOverlapOcc.End {
				overlap = true
				break
			}
		}

		if overlap {
			// reset lines done
			for l := occ.Start; l < occ.End; l++ {
				occ.fileToCheck.linesDone.set(l, false)
			}

			continue
		}

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

// lineIndex returns the line index and similarity level of needle in file, starting with startLine, according to opts.
// If no match can be found, -1 is returned for the line index.
func lineIndex(ctx context.Context, file *fileToCheck, needle *fileLine, startLine int, opts *Options) (int, SimilarityLevel) { //nolint:gocognit,cyclop // concurrent setup is complex
	linesToCheck := len(file.f.lines) - startLine

	if linesToCheck <= 0 {
		return -1, differentSimilarityLevel
	}

	const chunkSize = 10

	chunks := linesToCheck / chunkSize
	if chunks*chunkSize < linesToCheck {
		chunks++
	}

	if chunks == 1 {
		return lineIndexEnd(ctx, file, needle, startLine, len(file.f.lines), opts)
	}

	startLines := make([]int, chunks)
	for i := range startLines {
		startLines[i] = chunkSize*i + startLine
	}

	endLines := make([]int, chunks)
	for i := range endLines {
		endLines[i] = chunkSize*(i+1) + startLine
	}

	if endLines[len(endLines)-1] > len(file.f.lines) {
		endLines[len(endLines)-1] = len(file.f.lines)
	}

	contexts := make([]context.Context, chunks)
	cancels := make([]context.CancelFunc, chunks)

	for i := range contexts {
		contexts[i], cancels[i] = context.WithCancel(ctx)
	}

	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
	}()

	type result struct {
		line  int
		level SimilarityLevel
	}

	resultCh := make(chan result)

	grp := sync.WaitGroup{}
	grp.Add(chunks)

	for chunkIdx := 0; chunkIdx < chunks; chunkIdx++ {
		go func(ctx context.Context, startLine int, endLine int) {
			defer grp.Done()

			line, level := lineIndexEnd(ctx, file, needle, startLine, endLine, opts)
			resultCh <- result{line, level}
		}(contexts[chunkIdx], startLines[chunkIdx], endLines[chunkIdx])
	}

	go func() {
		defer close(resultCh)

		grp.Wait()
	}()

	smallestResult := result{
		line:  -1,
		level: differentSimilarityLevel,
	}

	for res := range resultCh {
		if res.line < 0 {
			continue
		}

		if smallestResult.line >= 0 && smallestResult.line <= res.line {
			continue
		}

		smallestResult = res

		for i, startLine := range startLines {
			if startLine > res.line {
				cancels[i]()
			}
		}
	}

	return smallestResult.line, smallestResult.level
}

// lineIndexEnd returns the line index and similarity level of needle in file, starting with startLine,
// ending with endLine (excluding), according to opts. If no match can be found, -1 is returned for the line index.
func lineIndexEnd(ctx context.Context, file *fileToCheck, needle *fileLine, startLine int, endLine int, opts *Options) (int, SimilarityLevel) {
	for lineIdx := startLine; ; lineIdx++ {
		if contextDone(ctx) {
			return -1, differentSimilarityLevel
		}

		if lineIdx >= endLine {
			return -1, differentSimilarityLevel
		}

		if file.linesDone.isSet(lineIdx) {
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
	if fileLine1.flags.set(matchesAlwaysDifferentLineFlag) || fileLine2.flags.set(matchesAlwaysDifferentLineFlag) {
		return differentSimilarityLevel
	}

	line1 := fileLine1.text
	line2 := fileLine2.text

	if opts.flagSet(IgnoreWhitespaceFlag) {
		line1 = fileLine1.textTrimmed
		line2 = fileLine2.textTrimmed
	}

	if line1 == line2 {
		return EqualSimilarityLevel
	}

	maxDist := opts.MaxEditDistance
	if maxDist <= 0 {
		maxDist = DefaultMaxEditDistance
	}

	if levenshteinDistance(fileLine1, fileLine2, opts) > maxDist {
		return differentSimilarityLevel
	}

	return SimilarSimilarityLevel
}

// levenshteinDistance returns the Levenshtein distance between line1 and line2.
func levenshteinDistance(fileLine1 *fileLine, fileLine2 *fileLine, opts *Options) int {
	slow := fileLine1.flagSet(slowLevenshteinLineFlag) || fileLine2.flagSet(slowLevenshteinLineFlag)

	if slow {
		line1 := fileLine1.text
		line2 := fileLine2.text

		if opts.flagSet(IgnoreWhitespaceFlag) {
			line1 = fileLine1.textTrimmed
			line2 = fileLine2.textTrimmed
		}

		return slowlevenshtein.Distance(line1, line2, nil)
	}

	line1 := fileLine1.textRunes
	line2 := fileLine2.textRunes

	if opts.flagSet(IgnoreWhitespaceFlag) {
		line1 = fileLine1.textTrimmedRunes
		line2 = fileLine2.textTrimmedRunes
	}

	return levenshtein.Distance(line1, line2)
}

// load loads all lines from f, and sets up f accordingly, such as setting flags.
func (f *File) load(opts *Options) error {
	f.lines = map[int]*fileLine{}

	reader := bufio.NewReader(f.R)
	buf := bytes.Buffer{}

	for lineIdx := 0; ; lineIdx++ {
		text, err := tsio.ReadLine(reader, &buf)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}

			return fmt.Errorf("read line: %w", err)
		}

		line := textToFileLine(text, opts)
		f.lines[lineIdx] = line
	}
}

func textToFileLine(text string, opts *Options) *fileLine { //nolint:cyclop // it's not too bad
	line := fileLine{
		text:        text,
		textTrimmed: strings.TrimSpace(text),
		textRunes:   []rune(text),
	}

	line.length = len(line.textRunes)

	if line.text != line.textTrimmed {
		line.textTrimmedRunes = []rune(line.textTrimmed)
		line.lengthTrimmed = len(line.textTrimmedRunes)
	} else {
		line.textTrimmed = line.text
		line.textTrimmedRunes = line.textRunes
		line.lengthTrimmed = line.length
	}

	if needsSlowLevenshtein(line.text) {
		line.flags |= slowLevenshteinLineFlag
	}

	if line.lengthTrimmed == 0 {
		line.flags |= blankLineFlag
	}

	if opts.IgnoreLineRegex != nil || opts.AlwaysDifferentLineRegex != nil {
		text = line.text
		if opts.flagSet(IgnoreWhitespaceFlag) {
			text = line.textTrimmed
		}

		if opts.IgnoreLineRegex != nil && opts.IgnoreLineRegex.MatchString(text) {
			line.flags |= matchesIgnoreRegexLineFlag
		}

		if opts.AlwaysDifferentLineRegex != nil && opts.AlwaysDifferentLineRegex.MatchString(text) {
			line.flags |= matchesAlwaysDifferentLineFlag
		}
	}

	return &line
}

// needsSlowLevenshtein returns whether a slower Levenshtein distance comparison must be used to compare s
// to any other string. This is the case if s contains any rune >65535.
func needsSlowLevenshtein(s string) bool {
	for _, r := range s {
		if r > 65535 {
			return true
		}
	}

	return false
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
	if opts.MinLineLength == 0 {
		return true
	}

	if l.flagSet(blankLineFlag) {
		return true
	}

	length := l.length
	if opts.flagSet(IgnoreWhitespaceFlag) {
		length = l.lengthTrimmed
	}

	return length >= opts.MinLineLength
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
