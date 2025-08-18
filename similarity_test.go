package textsimilarity

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/matryer/is"
)

type testingTOrB interface {
	Helper()
	Fatal(args ...any)
}

func TestSimilarities(t *testing.T) {
	is := is.New(t)

	file1 := newFile("1.txt", "aaaaaaaaaa\nbbbbbbbbbb\ncccccccccc\nxxxxxxxxxx\ncccccccccc\n")
	file2 := newFile("2.txt", "aaaaaaaaaa\nbbbbbbbbbb\n  cccccccccc  \ndddddddddd\ncccccxcccc\n")

	simsCh, progressCh, _ := Similarities(context.Background(), []*File{file1, file2}, &Options{MaxEditDistance: 2})

	var sims []*Similarity

	waitForAll(func() {
		sims = readSimilaritiesChan(simsCh)
	}, drainProgressChan(progressCh))

	is.Equal(len(sims), 2)

	is.Equal(len(sims[0].Occurrences), 2)
	is.Equal(sims[0].Level, EqualSimilarityLevel)

	is.Equal(sims[0].Occurrences[0].File, file1)
	is.Equal(sims[0].Occurrences[0].Start, 0)
	is.Equal(sims[0].Occurrences[0].End, 2)

	is.Equal(sims[0].Occurrences[1].File, file2)
	is.Equal(sims[0].Occurrences[1].Start, 0)
	is.Equal(sims[0].Occurrences[1].End, 2)

	is.Equal(len(sims[1].Occurrences), 3)
	is.Equal(sims[1].Level, SimilarSimilarityLevel)

	is.Equal(sims[1].Occurrences[0].File, file1)
	is.Equal(sims[1].Occurrences[0].Start, 2)
	is.Equal(sims[1].Occurrences[0].End, 3)

	is.Equal(sims[1].Occurrences[1].File, file1)
	is.Equal(sims[1].Occurrences[1].Start, 4)
	is.Equal(sims[1].Occurrences[1].End, 5)

	is.Equal(sims[1].Occurrences[2].File, file2)
	is.Equal(sims[1].Occurrences[2].Start, 4)
	is.Equal(sims[1].Occurrences[2].End, 5)
}

func TestSimilarities_IgnoreWhitespace(t *testing.T) {
	is := is.New(t)

	file1 := newFile("1.txt", "aaaaaaaaaa\nbbbbbbbbbb\ncccccccccc\nxxxxxxxxxx\ncccccccccc\n")
	file2 := newFile("2.txt", "aaaaaaaaaa\nbbbbbbbbbb\n  cccccccccc  \ndddddddddd\ncccccxcccc\n")

	simsCh, progressCh, _ := Similarities(context.Background(), []*File{file1, file2}, &Options{
		Flags:           IgnoreWhitespaceFlag,
		MaxEditDistance: 2,
	})

	var sims []*Similarity

	waitForAll(func() {
		sims = readSimilaritiesChan(simsCh)
	}, drainProgressChan(progressCh))

	is.Equal(len(sims), 2)

	is.Equal(len(sims[0].Occurrences), 2)

	is.Equal(sims[0].Occurrences[0].File, file1)
	is.Equal(sims[0].Occurrences[0].Start, 0)
	is.Equal(sims[0].Occurrences[0].End, 3)

	is.Equal(sims[0].Occurrences[1].File, file2)
	is.Equal(sims[0].Occurrences[1].Start, 0)
	is.Equal(sims[0].Occurrences[1].End, 3)

	is.Equal(len(sims[1].Occurrences), 2)

	is.Equal(sims[1].Occurrences[0].File, file1)
	is.Equal(sims[1].Occurrences[0].Start, 4)
	is.Equal(sims[1].Occurrences[0].End, 5)

	is.Equal(sims[1].Occurrences[1].File, file2)
	is.Equal(sims[1].Occurrences[1].Start, 4)
	is.Equal(sims[1].Occurrences[1].End, 5)
}

func TestSimilarities_IgnoreBlankLines(t *testing.T) {
	is := is.New(t)

	file1 := newFile("1.txt", "xxxxxxxxxx\naaaaaaaaaa\nbbbbbbbbbb\n")
	file2 := newFile("2.txt", "yyyyyyyyyy\nzzzzzzzzzz\naaaaaaaaaa\n\nbbbbbbbbbb\n")

	simsCh, progressCh, _ := Similarities(context.Background(), []*File{file1, file2}, &Options{
		Flags:           IgnoreBlankLinesFlag,
		MaxEditDistance: 2,
	})

	var sims []*Similarity

	waitForAll(func() {
		sims = readSimilaritiesChan(simsCh)
	}, drainProgressChan(progressCh))

	is.Equal(len(sims), 1)

	is.Equal(len(sims[0].Occurrences), 2)

	is.Equal(sims[0].Occurrences[0].File, file1)
	is.Equal(sims[0].Occurrences[0].Start, 1)
	is.Equal(sims[0].Occurrences[0].End, 3)

	is.Equal(sims[0].Occurrences[1].File, file2)
	is.Equal(sims[0].Occurrences[1].Start, 2)
	is.Equal(sims[0].Occurrences[1].End, 5)
}

func TestSimilarities_IgnoreRegex(t *testing.T) {
	is := is.New(t)

	file1 := newFile("1.txt", "aaaaaaaaaa\nfoo\nbbbbbbbbbb\ncccccccccc\n")
	file2 := newFile("2.txt", "aaaaaaaaaa\nbbbbbbbbbb\nbar\ncccccccccc\n")

	simsCh, progressCh, _ := Similarities(context.Background(), []*File{file1, file2}, &Options{
		IgnoreLineRegex: regexp.MustCompile("foo|bar"),
		MaxEditDistance: 2,
	})

	var sims []*Similarity

	waitForAll(func() {
		sims = readSimilaritiesChan(simsCh)
	}, drainProgressChan(progressCh))

	is.Equal(len(sims), 1)

	is.Equal(len(sims[0].Occurrences), 2)

	is.Equal(sims[0].Occurrences[0].File, file1)
	is.Equal(sims[0].Occurrences[0].Start, 0)
	is.Equal(sims[0].Occurrences[0].End, 4)

	is.Equal(sims[0].Occurrences[1].File, file2)
	is.Equal(sims[0].Occurrences[1].Start, 0)
	is.Equal(sims[0].Occurrences[1].End, 4)
}

func TestSimilarities_AlwaysDifferentRegex(t *testing.T) {
	is := is.New(t)

	file1 := newFile("1.txt", "aaaaaaaaaa\nfoo\nbbbbbbbbbb\ncccccccccc\n")
	file2 := newFile("2.txt", "aaaaaaaaaa\nfoo\nbbbbbbbbbb\ncccccccccc\n")

	simsCh, progressCh, _ := Similarities(context.Background(), []*File{file1, file2}, &Options{
		AlwaysDifferentLineRegex: regexp.MustCompile("foo"),
	})

	var sims []*Similarity

	waitForAll(func() {
		sims = readSimilaritiesChan(simsCh)
	}, drainProgressChan(progressCh))

	is.Equal(len(sims), 2)

	is.Equal(len(sims[0].Occurrences), 2)

	is.Equal(sims[0].Occurrences[0].File, file1)
	is.Equal(sims[0].Occurrences[0].Start, 0)
	is.Equal(sims[0].Occurrences[0].End, 1)

	is.Equal(sims[0].Occurrences[1].File, file2)
	is.Equal(sims[0].Occurrences[1].Start, 0)
	is.Equal(sims[0].Occurrences[1].End, 1)

	is.Equal(sims[1].Occurrences[0].File, file1)
	is.Equal(sims[1].Occurrences[0].Start, 2)
	is.Equal(sims[1].Occurrences[0].End, 4)

	is.Equal(sims[1].Occurrences[1].File, file2)
	is.Equal(sims[1].Occurrences[1].Start, 2)
	is.Equal(sims[1].Occurrences[1].End, 4)
}

func TestSimilarities_MinLineLength(t *testing.T) {
	is := is.New(t)

	file1 := newFile("1.txt", "aaaaaaaaaa\nfoo\nbbbbbbbbbb\ncccccccccc\n")
	file2 := newFile("2.txt", "aaaaaaaaaa\nbbbbbbbbbb\nbar\ncccccccccc\n")

	simsCh, progressCh, _ := Similarities(context.Background(), []*File{file1, file2}, &Options{
		MinLineLength:   5,
		MaxEditDistance: 2,
	})

	var sims []*Similarity

	waitForAll(func() {
		sims = readSimilaritiesChan(simsCh)
	}, drainProgressChan(progressCh))

	is.Equal(len(sims), 1)

	is.Equal(len(sims[0].Occurrences), 2)

	is.Equal(sims[0].Occurrences[0].File, file1)
	is.Equal(sims[0].Occurrences[0].Start, 0)
	is.Equal(sims[0].Occurrences[0].End, 4)

	is.Equal(sims[0].Occurrences[1].File, file2)
	is.Equal(sims[0].Occurrences[1].Start, 0)
	is.Equal(sims[0].Occurrences[1].End, 4)
}

func TestLinesSimilarity(t *testing.T) {
	tests := []struct {
		givenLine1 *fileLine
		givenLine2 *fileLine
		givenFlags Flag
		wantLevel  SimilarityLevel
	}{
		{
			givenLine1: newFileLine("aaaaaaaaaa", true),
			givenLine2: newFileLine("aaaaaaaaaa", true),
			wantLevel:  EqualSimilarityLevel,
		},
		{
			givenLine1: newFileLine("aaaaaaaaaa", true),
			givenLine2: newFileLine("bbbbbbbbbb", true),
			wantLevel:  differentSimilarityLevel,
		},
		{
			givenLine1: newFileLine("aaaaaaaaaa", true),
			givenLine2: newFileLine("     aaaaaaaaaa     ", true),
			givenFlags: IgnoreWhitespaceFlag,
			wantLevel:  EqualSimilarityLevel,
		},
		{
			givenLine1: newFileLine("aaaaaaaaaa", true),
			givenLine2: newFileLine("aaaaxaaaaa", true),
			wantLevel:  SimilarSimilarityLevel,
		},
	}

	for i, test := range tests {
		t.Run(fmt.Sprintf("[%d] line1=%+v, line2=%+v, ignoreWS=%t", i, test.givenLine1, test.givenLine2, test.givenFlags&IgnoreWhitespaceFlag == IgnoreWhitespaceFlag), func(t *testing.T) {
			is := is.New(t)
			is.Equal(linesSimilarity(test.givenLine1, test.givenLine2, &Options{Flags: test.givenFlags, MaxEditDistance: 2}), test.wantLevel)
		})
	}
}

func TestLineIndex(t *testing.T) {
	tests := []struct {
		description    string
		givenFile      *fileToCheck
		givenNeedle    *fileLine
		givenStartLine int
		wantLine       int
		wantLevel      SimilarityLevel
	}{
		{
			description: "found (first)",
			givenFile: newFileToCheck(t,
				[]string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "dddddddddd", "eeeeeeeeee"},
				[]bool{false, false, false, false, false},
				false,
			),
			givenNeedle: newFileLine("aaaaaaaaaa", false),
			wantLine:    0,
			wantLevel:   EqualSimilarityLevel,
		},
		{
			description: "found (middle)",
			givenFile: newFileToCheck(t,
				[]string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "dddddddddd", "eeeeeeeeee"},
				[]bool{false, false, false, false, false},
				false,
			),
			givenNeedle: newFileLine("bbbbbbbbbb", false),
			wantLine:    1,
			wantLevel:   EqualSimilarityLevel,
		},
		{
			description: "found (last)",
			givenFile: newFileToCheck(t,
				[]string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "dddddddddd", "eeeeeeeeee"},
				[]bool{false, false, false, false, false},
				false,
			),
			givenNeedle: newFileLine("eeeeeeeeee", false),
			wantLine:    4,
			wantLevel:   EqualSimilarityLevel,
		},
		{
			description: "not found",
			givenFile: newFileToCheck(t,
				[]string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "dddddddddd", "eeeeeeeeee"},
				[]bool{false, false, false, false, false},
				false,
			),
			givenNeedle: newFileLine("xxxxxxxxxx", false),
			wantLine:    -1,
		},
		{
			description: "found (startLine > 0)",
			givenFile: newFileToCheck(t,
				[]string{"aaaaaaaaaa", "bbbbbbbbbb", "aaaaaaaaaa", "dddddddddd", "eeeeeeeeee"},
				[]bool{false, false, false, false, false},
				false,
			),
			givenNeedle:    newFileLine("aaaaaaaaaa", false),
			givenStartLine: 1,
			wantLine:       2,
			wantLevel:      EqualSimilarityLevel,
		},
		{
			description: "not found (startLine > 0)",
			givenFile: newFileToCheck(t,
				[]string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "dddddddddd", "eeeeeeeeee"},
				[]bool{false, false, false, false, false},
				false,
			),
			givenNeedle:    newFileLine("aaaaaaaaaa", false),
			givenStartLine: 1,
			wantLine:       -1,
		},
		{
			description: "found (linesDone)",
			givenFile: newFileToCheck(t,
				[]string{"aaaaaaaaaa", "bbbbbbbbbb", "aaaaaaaaaa", "dddddddddd", "eeeeeeeeee"},
				[]bool{true, false, false, false, false},
				false,
			),
			givenNeedle: newFileLine("aaaaaaaaaa", false),
			wantLine:    2,
			wantLevel:   EqualSimilarityLevel,
		},
		{
			description: "not found (linesDone)",
			givenFile: newFileToCheck(t,
				[]string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "dddddddddd", "eeeeeeeeee"},
				[]bool{true, false, false, false, false},
				false,
			),
			givenNeedle: newFileLine("aaaaaaaaaa", false),
			wantLine:    -1,
		},
		{
			description: "found (similar)",
			givenFile: newFileToCheck(t,
				[]string{"aaaaaxaaaa", "bbbbbbbbbb", "cccccccccc", "dddddddddd", "eeeeeeeeee"},
				[]bool{false, false, false, false, false},
				false,
			),
			givenNeedle: newFileLine("aaaaaaaaaa", false),
			wantLine:    0,
			wantLevel:   SimilarSimilarityLevel,
		},
	}

	for i, test := range tests {
		t.Run(fmt.Sprintf("[%d] %s", i, test.description), func(t *testing.T) {
			is := is.New(t)

			line, level := lineIndex(context.Background(), test.givenFile, test.givenNeedle, test.givenStartLine, &Options{MaxEditDistance: 2})

			if test.wantLine < 0 {
				is.True(line < 0)
				return
			}

			is.Equal(line, test.wantLine)
			is.Equal(level, test.wantLevel)
		})
	}
}

func TestLineIndex_Large(t *testing.T) {
	is := is.New(t)

	osFile, _ := os.Open("testdata/lipsum.txt")
	defer osFile.Close() //nolint:errcheck // file is being read

	data, _ := io.ReadAll(osFile)
	texts := strings.Split(string(data), "\n")

	file := newFileToCheck(t, texts, make([]bool, len(texts)), false)

	needle := newFileLine(texts[50][:10]+"x"+texts[50][10:], false)

	opts := Options{MaxEditDistance: 2}

	line, level := lineIndex(context.Background(), file, needle, 0, &opts)

	is.Equal(line, 50)
	is.Equal(level, SimilarSimilarityLevel)
}

func TestExpandOccurrences(t *testing.T) {
	tests := []struct {
		description      string
		givenOccurrences []*FileOccurrence
		givenFlags       Flag
		wantEnds         []int
		wantLevel        SimilarityLevel
	}{
		{
			description: "whole files",
			givenOccurrences: []*FileOccurrence{
				{
					fileToCheck: newFileToCheck(t,
						[]string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "dddddddddd", "eeeeeeeeee"},
						[]bool{false, false, false, false, false},
						false,
					),
					Start: 0, End: 1,
				},
				{
					fileToCheck: newFileToCheck(t,
						[]string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "dddddddddd", "eeeeeeeeee"},
						[]bool{false, false, false, false, false},
						false,
					),
					Start: 0, End: 1,
				},
			},
			wantEnds:  []int{5, 5},
			wantLevel: EqualSimilarityLevel,
		},
		{
			description: "stop at WS diff",
			givenOccurrences: []*FileOccurrence{
				{
					fileToCheck: newFileToCheck(t,
						[]string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "dddddddddd", "eeeeeeeeee"},
						[]bool{false, false, false, false, false},
						false,
					),
					Start: 0, End: 1,
				},
				{
					fileToCheck: newFileToCheck(t,
						[]string{"aaaaaaaaaa", "bbbbbbbbbb", "     cccccccccc     ", "dddddddddd", "eeeeeeeeee"},
						[]bool{false, false, false, false, false},
						false,
					),
					Start: 0, End: 1,
				},
			},
			wantEnds:  []int{2, 2},
			wantLevel: EqualSimilarityLevel,
		},
		{
			description: "ignore WS",
			givenOccurrences: []*FileOccurrence{
				{
					fileToCheck: newFileToCheck(t,
						[]string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "dddddddddd", "eeeeeeeeee"},
						[]bool{false, false, false, false, false},
						true,
					),
					Start: 0, End: 1,
				},
				{
					fileToCheck: newFileToCheck(t,
						[]string{"aaaaaaaaaa", "bbbbbbbbbb", "     cccccccccc     ", "dddddddddd", "eeeeeeeeee"},
						[]bool{false, false, false, false, false},
						true,
					),
					Start: 0, End: 1,
				},
			},
			givenFlags: IgnoreWhitespaceFlag,
			wantEnds:   []int{5, 5},
			wantLevel:  EqualSimilarityLevel,
		},
		{
			description: "stop at blank line",
			givenOccurrences: []*FileOccurrence{
				{
					fileToCheck: newFileToCheck(t,
						[]string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "dddddddddd", "eeeeeeeeee"},
						[]bool{false, false, false, false, false},
						false,
					),
					Start: 0, End: 1,
				},
				{
					fileToCheck: newFileToCheck(t,
						[]string{"aaaaaaaaaa", "bbbbbbbbbb", "", "cccccccccc", "dddddddddd", "eeeeeeeeee"},
						[]bool{false, false, false, false, false, false},
						false,
					),
					Start: 0, End: 1,
				},
			},
			wantEnds:  []int{2, 2},
			wantLevel: EqualSimilarityLevel,
		},
		{
			description: "ignore blank lines",
			givenOccurrences: []*FileOccurrence{
				{
					fileToCheck: newFileToCheck(t,
						[]string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "dddddddddd", "eeeeeeeeee"},
						[]bool{false, false, false, false, false},
						false,
					),
					Start: 0, End: 1,
				},
				{
					fileToCheck: newFileToCheck(t,
						[]string{"aaaaaaaaaa", "bbbbbbbbbb", "", "cccccccccc", "dddddddddd", "eeeeeeeeee"},
						[]bool{false, false, false, false, false, false},
						false,
					),
					Start: 0, End: 1,
				},
			},
			givenFlags: IgnoreBlankLinesFlag,
			wantEnds:   []int{5, 6},
			wantLevel:  EqualSimilarityLevel,
		},
		{
			description: "stop at line done (occurrence #1)",
			givenOccurrences: []*FileOccurrence{
				{
					fileToCheck: newFileToCheck(t,
						[]string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "dddddddddd", "eeeeeeeeee"},
						[]bool{false, false, true, false, false},
						false,
					),
					Start: 0, End: 1,
				},
				{
					fileToCheck: newFileToCheck(t,
						[]string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "dddddddddd", "eeeeeeeeee"},
						[]bool{false, false, false, false, false},
						false,
					),
					Start: 0, End: 1,
				},
			},
			wantEnds:  []int{2, 2},
			wantLevel: EqualSimilarityLevel,
		},
		{
			description: "stop at line done (occurrence #2)",
			givenOccurrences: []*FileOccurrence{
				{
					fileToCheck: newFileToCheck(t,
						[]string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "dddddddddd", "eeeeeeeeee"},
						[]bool{false, false, false, false, false},
						false,
					),
					Start: 0, End: 1,
				},
				{
					fileToCheck: newFileToCheck(t,
						[]string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "dddddddddd", "eeeeeeeeee"},
						[]bool{false, false, true, false, false},
						false,
					),
					Start: 0, End: 1,
				},
			},
			wantEnds:  []int{2, 2},
			wantLevel: EqualSimilarityLevel,
		},
		{
			description: "stop at line done (ignore blank lines)",
			givenOccurrences: []*FileOccurrence{
				{
					fileToCheck: newFileToCheck(t,
						[]string{"aaaaaaaaaa", "bbbbbbbbbb", "", "cccccccccc", "dddddddddd", "eeeeeeeeee"},
						[]bool{false, false, false, false, false, false},
						false,
					),
					Start: 0, End: 1,
				},
				{
					fileToCheck: newFileToCheck(t,
						[]string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "dddddddddd", "eeeeeeeeee"},
						[]bool{false, false, false, true, false},
						false,
					),
					Start: 0, End: 1,
				},
			},
			givenFlags: IgnoreBlankLinesFlag,
			wantEnds:   []int{4, 3},
			wantLevel:  EqualSimilarityLevel,
		},
		{
			description: "similar",
			givenOccurrences: []*FileOccurrence{
				{
					fileToCheck: newFileToCheck(t,
						[]string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "dddddddddd", "eeeeeeeeee"},
						[]bool{false, false, false, false, false},
						false,
					),
					Start: 0, End: 1,
				},
				{
					fileToCheck: newFileToCheck(t,
						[]string{"aaaaaxaaaa", "bbbbbbbbbb", "cccccxcccc", "dddddddddd", "eeeeeexeee"},
						[]bool{false, false, false, false, false},
						false,
					),
					Start: 0, End: 1,
				},
			},
			wantEnds:  []int{5, 5},
			wantLevel: SimilarSimilarityLevel,
		},
	}

	for i, test := range tests {
		t.Run(fmt.Sprintf("[%d] %s", i, test.description), func(t *testing.T) {
			is := is.New(t)

			level := expandOccurrences(context.Background(), test.givenOccurrences, EqualSimilarityLevel, &Options{Flags: test.givenFlags, MaxEditDistance: 2})

			for i, o := range test.givenOccurrences {
				is.Equal(o.End, test.wantEnds[i])
			}

			is.Equal(level, test.wantLevel)
		})
	}
}

func TestLineOccurrences(t *testing.T) {
	tests := []struct {
		description     string
		givenFile       *fileToCheck
		givenLine       *fileLine
		givenStartLine  int
		wantOccurrences []*FileOccurrence
		wantLevel       SimilarityLevel
	}{
		{
			description:     "single",
			givenFile:       newFileToCheck(t, []string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "dddddddddd", "eeeeeeeeee"}, []bool{false, false, false, false, false}, false),
			givenLine:       newFileLine("aaaaaaaaaa", false),
			wantOccurrences: []*FileOccurrence{{Start: 0, End: 1}},
			wantLevel:       EqualSimilarityLevel,
		},
		{
			description:     "multiple",
			givenFile:       newFileToCheck(t, []string{"aaaaaaaaaa", "bbbbbbbbbb", "aaaaaaaaaa", "aaaaaaaaaa", "eeeeeeeeee"}, []bool{false, false, false, false, false}, false),
			givenLine:       newFileLine("aaaaaaaaaa", false),
			wantOccurrences: []*FileOccurrence{{Start: 0, End: 1}, {Start: 2, End: 3}, {Start: 3, End: 4}},
			wantLevel:       EqualSimilarityLevel,
		},
		{
			description:     "startLine > 0",
			givenFile:       newFileToCheck(t, []string{"aaaaaaaaaa", "bbbbbbbbbb", "aaaaaaaaaa", "dddddddddd", "eeeeeeeeee"}, []bool{false, false, false, false, false}, false),
			givenLine:       newFileLine("aaaaaaaaaa", false),
			givenStartLine:  1,
			wantOccurrences: []*FileOccurrence{{Start: 2, End: 3}},
			wantLevel:       EqualSimilarityLevel,
		},
		{
			description:     "similar",
			givenFile:       newFileToCheck(t, []string{"aaaaaxaaaa", "bbbbbbbbbb", "cccccccccc", "dddddddddd", "eeeeeeeeee"}, []bool{false, false, false, false, false}, false),
			givenLine:       newFileLine("aaaaaaaaaa", false),
			wantOccurrences: []*FileOccurrence{{Start: 0, End: 1}},
			wantLevel:       SimilarSimilarityLevel,
		},
	}

	for i, test := range tests {
		t.Run(fmt.Sprintf("[%d] %s", i, test.description), func(t *testing.T) {
			is := is.New(t)

			occs, level := lineOccurrences(context.Background(), test.givenFile, test.givenLine, test.givenStartLine, &Options{MaxEditDistance: 2})

			is.Equal(len(occs), len(test.wantOccurrences))

			for i, occ := range occs {
				is.Equal(occ.fileToCheck, test.givenFile)
				is.Equal(occ.Start, test.wantOccurrences[i].Start)
				is.Equal(occ.End, test.wantOccurrences[i].End)
			}

			is.Equal(level, test.wantLevel)
		})
	}
}

func TestFileSimilarities_SingleFile_SingleSimilarity(t *testing.T) {
	givenFile := &File{
		Name: "test.txt",
	}

	lines := []string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "aaaaaaaaaa", "bbbbbbbbbb", "xxxxxxxxxx", "aaaaaaaaaa", "bbbbbbbbbb"}
	linesDone := []bool{false, false, false, false, false, false, false, false}

	givenFileToCheck := newFileToCheck(t, lines, linesDone, false)
	givenFileToCheck.peers = []*fileToCheck{newFileToCheck(t, lines, linesDone, false)}
	givenFileToCheck.peers[0].f = givenFileToCheck.f

	wantSimilarities := []*Similarity{
		{
			Occurrences: []*FileOccurrence{
				{File: givenFile, Start: 0, End: 2, fileToCheck: givenFileToCheck},
				{File: givenFile, Start: 3, End: 5, fileToCheck: givenFileToCheck.peers[0]},
				{File: givenFile, Start: 6, End: 8, fileToCheck: givenFileToCheck.peers[0]},
			},
			Level: EqualSimilarityLevel,
		},
	}

	testFileSimilarities(t, givenFileToCheck, 0, 0, wantSimilarities)
}

func TestFileSimilarities_SingleFile_MultipleSimilarities(t *testing.T) {
	givenFile := &File{
		Name: "test.txt",
	}

	lines := []string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "dddddddddd", "eeeeeeeeee", "aaaaaaaaaa", "bbbbbbbbbb", "ffffffffff", "dddddddddd"}
	linesDone := []bool{false, false, false, false, false, false, false, false, false}

	givenFileToCheck := newFileToCheck(t, lines, linesDone, false)
	givenFileToCheck.peers = []*fileToCheck{newFileToCheck(t, lines, linesDone, false)}
	givenFileToCheck.peers[0].f = givenFileToCheck.f

	wantSimilarities := []*Similarity{
		{
			Occurrences: []*FileOccurrence{
				{File: givenFile, Start: 0, End: 2, fileToCheck: givenFileToCheck},
				{File: givenFile, Start: 5, End: 7, fileToCheck: givenFileToCheck.peers[0]},
			},
			Level: EqualSimilarityLevel,
		},
		{
			Occurrences: []*FileOccurrence{
				{File: givenFile, Start: 3, End: 4, fileToCheck: givenFileToCheck},
				{File: givenFile, Start: 8, End: 9, fileToCheck: givenFileToCheck.peers[0]},
			},
			Level: EqualSimilarityLevel,
		},
	}

	testFileSimilarities(t, givenFileToCheck, 0, 0, wantSimilarities)
}

func TestFileSimilarities_MultipleFiles(t *testing.T) {
	givenFile1 := &File{
		Name: "test1.txt",
	}

	givenFile2 := &File{
		Name: "test2.txt",
	}

	lines1 := []string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "dddddddddd", "bbbbbbbbbb"}
	lines1Done := []bool{false, false, false, false, false}

	lines2 := []string{"wwwwwwwwww", "xxxxxxxxxx", "bbbbbbbbbb", "yyyyyyyyyy", "zzzzzzzzzz"}
	lines2Done := []bool{false, false, false, false, false}

	givenFileToCheck := newFileToCheck(t, lines1, lines1Done, false)
	givenFileToCheck.peers = []*fileToCheck{
		newFileToCheck(t, lines1, lines1Done, false),
		newFileToCheck(t, lines2, lines2Done, false),
	}
	givenFileToCheck.peers[0].f = givenFileToCheck.f

	wantSimilarities := []*Similarity{
		{
			Occurrences: []*FileOccurrence{
				{File: givenFile1, Start: 1, End: 2, fileToCheck: givenFileToCheck},
				{File: givenFile1, Start: 4, End: 5, fileToCheck: givenFileToCheck.peers[0]},
				{File: givenFile2, Start: 2, End: 3, fileToCheck: givenFileToCheck.peers[1]},
			},
			Level: EqualSimilarityLevel,
		},
	}

	testFileSimilarities(t, givenFileToCheck, 0, 0, wantSimilarities)
}

func TestFileSimilarities_IgnoreBlankLines(t *testing.T) {
	givenFile := &File{
		Name: "test.txt",
	}

	lines := []string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "aaaaaaaaaa", "", "bbbbbbbbbb", "xxxxxxxxxx", "aaaaaaaaaa", "bbbbbbbbbb"}
	linesDone := []bool{false, false, false, false, false, false, false, false, false}

	givenFileToCheck := newFileToCheck(t, lines, linesDone, false)
	givenFileToCheck.peers = []*fileToCheck{newFileToCheck(t, lines, linesDone, false)}
	givenFileToCheck.peers[0].f = givenFileToCheck.f

	wantSimilarities := []*Similarity{
		{
			Occurrences: []*FileOccurrence{
				{File: givenFile, Start: 0, End: 2, fileToCheck: givenFileToCheck},
				{File: givenFile, Start: 3, End: 6, fileToCheck: givenFileToCheck.peers[0]},
				{File: givenFile, Start: 7, End: 9, fileToCheck: givenFileToCheck.peers[0]},
			},
			Level: EqualSimilarityLevel,
		},
	}

	testFileSimilarities(t, givenFileToCheck, IgnoreBlankLinesFlag, 0, wantSimilarities)
}

func TestFileSimilarities_IgnoreRegex(t *testing.T) {
	givenFile := &File{
		Name: "test.txt",
	}

	lines := []string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc"}
	linesDone := []bool{false, false, false, false, false, false}

	givenFileToCheck := newFileToCheck(t, lines, linesDone, false)
	givenFileToCheck.f.lines[2].flags |= matchesIgnoreRegexLineFlag
	givenFileToCheck.peers = []*fileToCheck{newFileToCheck(t, lines, linesDone, false)}
	givenFileToCheck.peers[0].f = givenFileToCheck.f

	wantSimilarities := []*Similarity{
		{
			Occurrences: []*FileOccurrence{
				{File: givenFile, Start: 0, End: 2, fileToCheck: givenFileToCheck},
				{File: givenFile, Start: 3, End: 5, fileToCheck: givenFileToCheck.peers[0]},
			},
			Level: EqualSimilarityLevel,
		},
	}

	testFileSimilarities(t, givenFileToCheck, IgnoreBlankLinesFlag, 0, wantSimilarities)
}

func TestFileSimilarities_MinSimilarLines(t *testing.T) {
	givenFile1 := &File{
		Name: "test1.txt",
	}

	givenFile2 := &File{
		Name: "test2.txt",
	}

	lines1 := []string{"aaaaaaaaaa", "xxxxxxxxxx", "bbbbbbbbbb", "aaaaaaaaaa", "xxxxxxxxxx", "yyyyyyyyyy"}
	lines1Done := []bool{false, false, false, false, false, false}

	lines2 := []string{"aaaaaaaaaa", "xxxxxxxxxx", "yyyyyyyyyy"}
	lines2Done := []bool{false, false, false}

	givenFileToCheck := newFileToCheck(t, lines1, lines1Done, false)
	givenFileToCheck.peers = []*fileToCheck{
		newFileToCheck(t, lines1, lines1Done, false),
		newFileToCheck(t, lines2, lines2Done, false),
	}
	givenFileToCheck.peers[0].f = givenFileToCheck.f

	wantSimilarities := []*Similarity{
		{
			Occurrences: []*FileOccurrence{
				{File: givenFile1, Start: 3, End: 6, fileToCheck: givenFileToCheck},
				{File: givenFile2, Start: 0, End: 3, fileToCheck: givenFileToCheck.peers[1]},
			},
			Level: EqualSimilarityLevel,
		},
	}

	testFileSimilarities(t, givenFileToCheck, 0, 3, wantSimilarities)
}

func TestFileSimilarities_Similar(t *testing.T) {
	givenFile := &File{
		Name: "test.txt",
	}

	lines := []string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "aaaaxaaaaa", "bbbbbbbbbb", "xxxxxxxxxx", "aaaaaaaaaa", "bbbbbbbbbb"}
	linesDone := []bool{false, false, false, false, false, false, false, false}

	givenFileToCheck := newFileToCheck(t, lines, linesDone, false)
	givenFileToCheck.peers = []*fileToCheck{newFileToCheck(t, lines, linesDone, false)}
	givenFileToCheck.peers[0].f = givenFileToCheck.f

	wantSimilarities := []*Similarity{
		{
			Occurrences: []*FileOccurrence{
				{File: givenFile, Start: 0, End: 2, fileToCheck: givenFileToCheck},
				{File: givenFile, Start: 3, End: 5, fileToCheck: givenFileToCheck.peers[0]},
				{File: givenFile, Start: 6, End: 8, fileToCheck: givenFileToCheck.peers[0]},
			},
			Level: SimilarSimilarityLevel,
		},
	}

	testFileSimilarities(t, givenFileToCheck, 0, 0, wantSimilarities)
}

func TestFileSimilarities_SingleFile_Overlap(t *testing.T) {
	is := is.New(t)

	const content = `package foo

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("Hello World!")
}

type foobar struct {
	// some really long comment that goes on and on and on and on and on and on and on and on and on and on and on and on and on and on and on and on and on and on and on and on and on and on and on and on and on and on and on and on
	name string
}

func (s *foobar) GetName() string {
	return s.name
}

type foobar struct {
	// some really long comment that goes on and on and on and on and on and on and on and on and on and on and on and on and on and on and on and on and on and on and on and on and on and on and on and on and on and on and on and on
	name string
}

func (s *foobar) GetName() string {
	return s.name
}
`

	opts := Options{
		MinSimilarLines: 3,
		MaxEditDistance: 5,
	}

	givenFile := File{
		Name: "text",
		R:    strings.NewReader(content),
	}

	_ = givenFile.load(&opts)

	givenFileToCheck := fileToCheck{
		f:         &givenFile,
		linesDone: newBitVector(len(givenFile.lines)),
	}

	peerFile := fileToCheck{
		f:         &givenFile,
		linesDone: newBitVector(len(givenFile.lines)),
	}

	givenFileToCheck.peers = []*fileToCheck{&peerFile}

	sims := fileSimilarities(context.Background(), &givenFileToCheck, &opts)

	is.Equal(len(sims), 1)
	is.Equal(len(sims[0].Occurrences), 2)
	is.Equal(sims[0].Occurrences[0].Start, 11)
	is.Equal(sims[0].Occurrences[0].End, 19)
	is.Equal(sims[0].Occurrences[1].Start, 20)
	is.Equal(sims[0].Occurrences[1].End, 28)
}

func testFileSimilarities(t *testing.T, givenFile *fileToCheck, givenFlags Flag, givenMinSimilarLines int, wantSimilarities []*Similarity) {
	t.Helper()

	is := is.New(t)

	sims := fileSimilarities(context.Background(), givenFile, &Options{
		Flags:           givenFlags,
		MinSimilarLines: givenMinSimilarLines,
		MaxEditDistance: 2,
	})

	is.Equal(len(sims), len(wantSimilarities))

	for simIdx, sim := range sims {
		is.Equal(len(sim.Occurrences), len(wantSimilarities[simIdx].Occurrences))

		for occIdx, occ := range sim.Occurrences {
			is.Equal(occ.fileToCheck, wantSimilarities[simIdx].Occurrences[occIdx].fileToCheck)
			is.Equal(occ.Start, wantSimilarities[simIdx].Occurrences[occIdx].Start)
			is.Equal(occ.End, wantSimilarities[simIdx].Occurrences[occIdx].End)
		}

		is.Equal(sim.Level, wantSimilarities[simIdx].Level)
	}
}

func TestFile_Load(t *testing.T) {
	is := is.New(t)

	file := newFile("test.txt", "aaaaaaaaaa\nbbbbbbbbbb\nfoo\ncccccccccc\n𨊂\ndddddddddd\neeeeeeeeee\n")

	wantLines := newFileLinesSlice(t, []string{"aaaaaaaaaa", "bbbbbbbbbb", "foo", "cccccccccc", "𨊂", "dddddddddd", "eeeeeeeeee"}, false)

	_ = file.load(&Options{
		IgnoreLineRegex: regexp.MustCompile("foo"),
	})

	is.Equal(len(file.lines), len(wantLines))

	for i := 0; i < len(file.lines); i++ {
		is.Equal(file.lines[i].text, wantLines[i].text)
		is.Equal(file.lines[i].length, wantLines[i].length)
	}

	is.True(file.lines[2].flagSet(matchesIgnoreRegexLineFlag))

	is.True(file.lines[4].flagSet(slowLevenshteinLineFlag))
}

func TestFileLine_LongEnough(t *testing.T) {
	is := is.New(t)

	is.True(newFileLine("foo", false).longEnough(&Options{}))
	is.True(!newFileLine("foo", false).longEnough(&Options{MinLineLength: 5}))
	is.True(newFileLine("", false).longEnough(&Options{}))
	is.True(newFileLine("", false).longEnough(&Options{MinLineLength: 5}))
	is.True(newFileLine("  foo  ", true).longEnough(&Options{Flags: IgnoreWhitespaceFlag, MinLineLength: 3}))
}

func newFile(name string, text string) *File {
	return &File{
		Name: name,
		R:    strings.NewReader(text),
	}
}

func newFileToCheck(t testingTOrB, texts []string, done []bool, ignoreWS bool) *fileToCheck {
	t.Helper()

	if len(texts) != len(done) {
		t.Fatal("len(texts) != len(done)")
	}

	linesDone := newBitVector(len(done))
	for i, d := range done {
		linesDone.set(i, d)
	}

	return &fileToCheck{
		f: &File{
			lines: newFileLinesSlice(t, texts, ignoreWS),
		},
		linesDone: linesDone,
	}
}

func newFileLinesSlice(t testingTOrB, texts []string, ignoreWS bool) []*fileLine {
	t.Helper()

	lines := make([]*fileLine, len(texts))
	for i, t := range texts {
		lines[i] = newFileLine(t, ignoreWS)
	}

	return lines
}

func newFileLine(text string, ignoreWS bool) *fileLine {
	if ignoreWS {
		text = strings.TrimSpace(text)
	}

	textRunes := []rune(text)
	length := len(textRunes)

	line := fileLine{
		text:      text,
		textRunes: textRunes,
		length:    length,
	}

	if length == 0 {
		line.flags |= blankLineFlag
	}

	return &line
}

func readSimilaritiesChan(ch <-chan *Similarity) []*Similarity {
	sims := []*Similarity{}

	for sim := range ch {
		sims = append(sims, sim)
	}

	return sims
}

func drainProgressChan(ch <-chan Progress) func() {
	return func() {
		for range ch { //nolint:revive // do nothing with channel contents
		}
	}
}

func waitForAll(funcs ...func()) {
	grp := sync.WaitGroup{}
	grp.Add(len(funcs))

	for _, f := range funcs {
		go func(f func()) {
			defer grp.Done()

			f()
		}(f)
	}

	grp.Wait()
}
