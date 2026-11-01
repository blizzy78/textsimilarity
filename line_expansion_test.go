package textsimilarity

import (
	"context"
	"fmt"
	"math/rand/v2"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func lineEditOccurrence(t *testing.T, name string, lines []string, opts *Options) *FileOccurrence {
	t.Helper()

	file := newFile(name, strings.Join(lines, "\n"))
	if err := file.load(opts); err != nil {
		t.Fatal(err)
	}

	return &FileOccurrence{File: file, Start: 0, End: 1, fileToCheck: &fileToCheck{f: file}}
}

func TestExpandLineEdits(t *testing.T) { //nolint:gocognit // table checks endpoints, levels, and speculative marker changes
	alpha, beta, gamma, delta, epsilon, phi, eta, extra, changed, other := "aaaaaaaaaaaa", "bbbbbbbbbbbb", "cccccccccccc", "dddddddddddd", "eeeeeeeeeeee", "ffffffffffff", "gggggggggggg", "xxxxxxxxxxxx", "yyyyyyyyyyyy", "zzzzzzzzzzzz"

	tests := []struct {
		name   string
		source []string
		peer   []string
		budget int
		ends   [2]int
		level  SimilarityLevel
	}{
		{"unchanged", []string{alpha, beta, gamma, delta}, []string{alpha, beta, gamma, delta}, 3, [2]int{4, 4}, EqualSimilarityLevel},
		{"inserted line", []string{alpha, beta, gamma, delta}, []string{alpha, extra, beta, gamma, delta}, 1, [2]int{4, 5}, SimilarSimilarityLevel},
		{"removed line", []string{alpha, beta, gamma, delta}, []string{alpha, gamma, delta}, 1, [2]int{4, 3}, SimilarSimilarityLevel},
		{"replacement costs one", []string{alpha, beta, gamma, delta}, []string{alpha, extra, gamma, delta}, 1, [2]int{4, 4}, SimilarSimilarityLevel},
		{"combined edits", []string{alpha, beta, gamma, delta, epsilon, phi}, []string{alpha, extra, beta, changed, delta, phi}, 3, [2]int{6, 6}, SimilarSimilarityLevel},
		{"exact matches reset the budget", []string{alpha, beta, gamma, delta, epsilon, phi, eta}, []string{alpha, extra, gamma, changed, epsilon, other, eta}, 2, [2]int{7, 7}, SimilarSimilarityLevel},
		{"single character changes cost one", []string{alpha, beta, gamma, delta}, []string{alpha, "bbbbbxbbbbbb", "cccccxcccccc", extra}, 1, [2]int{2, 2}, SimilarSimilarityLevel},
		{"short peer", []string{alpha, beta, gamma, delta}, []string{alpha, beta}, 2, [2]int{4, 2}, SimilarSimilarityLevel},
		{"short peer exceeds budget", []string{alpha, beta, gamma, delta, epsilon}, []string{alpha, beta}, 1, [2]int{3, 2}, SimilarSimilarityLevel},
		{"strict mode", []string{alpha, beta, gamma, delta}, []string{alpha, extra, gamma, delta}, 0, [2]int{1, 1}, EqualSimilarityLevel},
		{"strict mode rejects one character", []string{alpha, beta, gamma}, []string{alpha, "bbbbbbbbbbbx", gamma}, 0, [2]int{1, 1}, EqualSimilarityLevel},
		{"four consecutive edits stop", []string{alpha, beta, gamma, delta, epsilon, phi}, []string{alpha, extra, changed, other, eta, phi}, 3, [2]int{4, 4}, SimilarSimilarityLevel},
		{"three edits then reset", []string{alpha, beta, gamma, delta, epsilon, phi, eta}, []string{alpha, extra, changed, other, epsilon, other, eta}, 3, [2]int{7, 7}, SimilarSimilarityLevel},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opts := &Options{MaxLineDistance: test.budget}
			occs := []*FileOccurrence{lineEditOccurrence(t, "alpha", test.source, opts), lineEditOccurrence(t, "beta", test.peer, opts)}

			level := expandOccurrences(t.Context(), occs, EqualSimilarityLevel, opts)
			if ends := [2]int{occs[0].End, occs[1].End}; ends != test.ends || level != test.level {
				t.Fatalf("got ends %v, level %v; want %v, %v", ends, level, test.ends, test.level)
			}

			for _, occ := range occs {
				for line := range occ.fileToCheck.f.lines {
					want := test.ends[0] > 1 && line < occ.End
					if occ.fileToCheck.isDone(line) != want {
						t.Fatalf("%s line %delta: speculative alignment changed markers", occ.File.Name, line)
					}
				}
			}
		})
	}
}

func TestLineEditBudgetPerOccurrence(t *testing.T) {
	opts := &Options{MaxLineDistance: 1}
	source := []string{"aaaaaaaaaaaa", "bbbbbbbbbbbb", "cccccccccccc", "dddddddddddd"}
	first := append([]string(nil), source...)
	second := append([]string(nil), source...)
	first[1], second[2] = "xxxxxxxxxxxx", "yyyyyyyyyyyy"

	occs := []*FileOccurrence{lineEditOccurrence(t, "alpha", source, opts), lineEditOccurrence(t, "beta", first, opts), lineEditOccurrence(t, "gamma", second, opts)}
	if level := expandOccurrences(t.Context(), occs, EqualSimilarityLevel, opts); level != SimilarSimilarityLevel {
		t.Fatal("edited occurrences were not classified as similar")
	}

	for _, occ := range occs {
		if occ.End != 4 {
			t.Fatal("line budgets were shared between occurrences")
		}
	}
}

func TestLineEditIgnoredLinesAndMarkers(t *testing.T) {
	opts := &Options{MaxLineDistance: 1, Flags: IgnoreBlankLinesFlag, IgnoreLineRegex: regexp.MustCompile("^ignored")}
	alpha, beta, gamma := "aaaaaaaaaaaa", "bbbbbbbbbbbb", "cccccccccccc"

	occs := []*FileOccurrence{
		lineEditOccurrence(t, "alpha", []string{alpha, "", "ignored source", beta, gamma}, opts),
		lineEditOccurrence(t, "beta", []string{alpha, "xxxxxxxxxxxx", beta, "ignored peer", "", gamma}, opts),
	}
	if level := expandOccurrences(t.Context(), occs, EqualSimilarityLevel, opts); level != SimilarSimilarityLevel || occs[0].End != 5 || occs[1].End != 6 {
		t.Fatal("ignored lines consumed the line-edit budget")
	}

	opts.Flags, opts.IgnoreLineRegex = 0, nil
	occs = []*FileOccurrence{lineEditOccurrence(t, "alpha", []string{alpha, beta, gamma, gamma}, opts), lineEditOccurrence(t, "beta", []string{alpha, beta, gamma, gamma}, opts)}
	occs[1].fileToCheck.setDone(2, true)
	expandOccurrences(t.Context(), occs, EqualSimilarityLevel, opts)

	if occs[1].End != 2 || !occs[1].fileToCheck.isDone(2) || occs[1].fileToCheck.isDone(3) {
		t.Fatal("alignment crossed alpha previously marked line")
	}
}

func TestLineEditCancellation(t *testing.T) {
	opts := &Options{MaxLineDistance: 3}
	lines := []string{"aaaaaaaaaaaa", "bbbbbbbbbbbb"}
	occs := []*FileOccurrence{lineEditOccurrence(t, "alpha", lines, opts), lineEditOccurrence(t, "beta", lines, opts)}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	expandOccurrences(ctx, occs, EqualSimilarityLevel, opts)

	if occs[0].End != 1 || occs[1].End != 1 || occs[0].fileToCheck.isDone(0) {
		t.Fatal("canceled expansion changed endpoints or markers")
	}
}

func TestLineEditsAlwaysDifferent(t *testing.T) {
	opts := &Options{MaxLineDistance: 1, AlwaysDifferentLineRegex: regexp.MustCompile("^barrier")}
	lines := []string{"seed seed seed", "barrier first", "matching matching", "barrier second", "tail tail tail"}
	occs := []*FileOccurrence{lineEditOccurrence(t, "first", lines, opts), lineEditOccurrence(t, "second", lines, opts)}
	expandOccurrences(t.Context(), occs, EqualSimilarityLevel, opts)

	if occs[0].End != 5 || occs[1].End != 5 {
		t.Fatal("always-different lines did not consume the line-edit budget")
	}
}

func TestLineEditIgnoredSeed(t *testing.T) {
	opts := &Options{MaxLineDistance: 1, IgnoreLineRegex: regexp.MustCompile("^a")}
	peer := lineEditOccurrence(t, "peer", []string{"aaaaaaaaaaaa", "baaaaaaaaaaa"}, opts)
	needle := textToFileLine("baaaaaaaaaaa", opts)

	line, _ := lineIndex(t.Context(), peer.fileToCheck, needle, 0, opts)
	if line != 1 {
		t.Fatal("an ignored line was used as the matching seed")
	}
}

func TestLineEditIgnoreBeforeAlwaysDifferent(t *testing.T) {
	opts := &Options{MaxLineDistance: 1, IgnoreLineRegex: regexp.MustCompile("^barrier"), AlwaysDifferentLineRegex: regexp.MustCompile("^barrier")}
	occs := []*FileOccurrence{
		lineEditOccurrence(t, "first", []string{"seed seed seed", "barrier first", "aaaaaaaaaaaa", "barrier second", "bbbbbbbbbbbb"}, opts),
		lineEditOccurrence(t, "second", []string{"seed seed seed", "aaaaaaaaaaaa", "xxxxxxxxxxxx"}, opts),
	}
	expandOccurrences(t.Context(), occs, EqualSimilarityLevel, opts)

	if occs[0].End != 5 || occs[1].End != 3 {
		t.Fatal("ignored lines consumed edits before the replacement")
	}
}

func TestLineEditLargeBudget(t *testing.T) {
	opts := &Options{MaxLineDistance: int(^uint(0) >> 1)}

	occs := []*FileOccurrence{
		lineEditOccurrence(t, "first", []string{"seed seed seed", "aaaaaaaaaaaa", "bbbbbbbbbbbb"}, opts),
		lineEditOccurrence(t, "second", []string{"seed seed seed", "xxxxxxxxxxxx", "yyyyyyyyyyyy"}, opts),
	}
	if level := expandOccurrences(t.Context(), occs, EqualSimilarityLevel, opts); level != SimilarSimilarityLevel || occs[0].End != 3 || occs[1].End != 3 {
		t.Fatal("large budget was not bounded by the actual input lengths")
	}
}

func TestLineEditWideGroup(t *testing.T) {
	opts := &Options{MaxLineDistance: 3}

	occs := []*FileOccurrence{lineEditOccurrence(t, "source", []string{"seed seed seed", "aaaaaaaaaaaa", "bbbbbbbbbbbb", "cccccccccccc"}, opts)}
	for idx := 0; idx < 5000; idx++ {
		occs = append(occs, lineEditOccurrence(t, fmt.Sprint(idx), []string{"seed seed seed"}, opts))
	}

	if level := expandOccurrences(t.Context(), occs, EqualSimilarityLevel, opts); level != SimilarSimilarityLevel || occs[0].End != 4 {
		t.Fatal("wide group did not retain its valid common prefix")
	}

	for _, peer := range occs[1:] {
		if peer.End != 1 {
			t.Fatal("alignment added nonexistent peer lines")
		}
	}
}

func TestLineEditGroupAgainstFullMatrices(t *testing.T) { //nolint:gocognit // independent matrices determine the common prefix and every peer endpoint
	random := rand.New(rand.NewPCG(151, 152)) //nolint:gosec // reproducible test inputs
	pool := []string{"aaaaaaaaaaaa", "aaaaabaaaaaa", "bbbbbbbbbbbb", "cccccccccccc"}

	for trial := 0; trial < 200; trial++ {
		opts := &Options{MaxLineDistance: 1 + random.IntN(3)}

		occs := make([]*FileOccurrence, 2+random.IntN(4))
		for idx := range occs {
			count := 1 + random.IntN(12)
			lines := make([]string, 1, count+1)

			lines[0] = "seed seed seed"
			for range count {
				lines = append(lines, pool[random.IntN(len(pool))])
			}

			occs[idx] = lineEditOccurrence(t, fmt.Sprint(idx), lines, opts)
		}

		commonEnd := len(occs[0].File.lines)
		for _, peer := range occs[1:] {
			ends, _ := referenceLineExpansion([]*FileOccurrence{occs[0], peer}, opts)
			commonEnd = min(commonEnd, ends[0])
		}

		limitedSource := *occs[0].File
		limitedSource.lines = limitedSource.lines[:commonEnd]
		wantEnds := make([]int, 1, len(occs))
		wantEnds[0] = commonEnd
		wantLevel := EqualSimilarityLevel

		for _, peer := range occs[1:] {
			ends, level := referenceLineExpansion([]*FileOccurrence{{File: &limitedSource}, peer}, opts)
			wantEnds = append(wantEnds, ends[1])
			wantLevel = min(wantLevel, level)
		}

		gotLevel := expandOccurrences(t.Context(), occs, EqualSimilarityLevel, opts)
		for idx, occ := range occs {
			if occ.End != wantEnds[idx] || gotLevel != wantLevel {
				t.Fatalf("trial %d peer %d: end/level %d/%v, want %d/%v", trial, idx, occ.End, gotLevel, wantEnds[idx], wantLevel)
			}
		}
	}
}

func TestLineEditSameFileCopies(t *testing.T) {
	opts := &Options{MaxLineDistance: 3}
	block := []string{"aaaaaaaaaaaa", "bbbbbbbbbbbb", "cccccccccccc", "dddddddddddd"}
	first := lineEditOccurrence(t, "same", append(append([]string(nil), block...), block...), opts)
	second := &FileOccurrence{File: first.File, Start: 4, End: 5, fileToCheck: &fileToCheck{f: first.File}}
	expandOccurrences(t.Context(), []*FileOccurrence{first, second}, EqualSimilarityLevel, opts)

	if first.End != 4 || second.End != 8 {
		t.Fatalf("neighboring copies overlapped: %delta, %delta", first.End, second.End)
	}
}

func TestLineAlignmentAgainstFullMatrix(t *testing.T) {
	random := rand.New(rand.NewPCG(121, 122)) //nolint:gosec // reproducible test inputs
	pool := []string{"aaaaaaaaaaaa", "aaaaabaaaaaa", "aaaaabbaaaaa", "bbbbbbbbbbbb", "cccccccccccc", "日本語の文字列", "\xff\xfe long invalid"}

	for trial := 0; trial < 1000; trial++ {
		opts := &Options{MaxLineDistance: 1 + random.IntN(4)}
		opts.MinSimilarLines = 1 + random.IntN(10)

		texts := make([][]string, 2)
		for idx := range texts {
			texts[idx] = []string{"seed seed seed"}
			for range random.IntN(10) {
				texts[idx] = append(texts[idx], pool[random.IntN(len(pool))])
			}
		}

		occs := []*FileOccurrence{lineEditOccurrence(t, "alpha", texts[0], opts), lineEditOccurrence(t, "beta", texts[1], opts)}
		wantEnds, wantLevel := referenceLineExpansion(occs, opts)

		possible := canReachMinimum(t.Context(), occs[0].fileToCheck, 0, occs[1].fileToCheck, 0, opts)
		if possible != (wantEnds[0] >= opts.MinSimilarLines) {
			t.Fatalf("trial %d: minimum-length probe disagrees with the full matrix", trial)
		}

		gotLevel := expandOccurrences(t.Context(), occs, EqualSimilarityLevel, opts)
		if got := [2]int{occs[0].End, occs[1].End}; got != wantEnds || gotLevel != wantLevel {
			t.Fatalf("trial %delta: ends %v level %v, want %v %v; inputs %q", trial, got, gotLevel, wantEnds, wantLevel, texts)
		}
	}
}

// Enumerate all valid edit-run lengths in a complete matrix. Unlike production,
// this retains dominated states and does not trim, stream, or lazily read input.
func referenceLineExpansion(occs []*FileOccurrence, opts *Options) ([2]int, SimilarityLevel) { //nolint:gocognit,cyclop // independent exhaustive alignment oracle
	type state struct {
		run    int
		edited bool
	}

	left, right := occs[0].File.lines[1:], occs[1].File.lines[1:]

	cells := make([][]map[state]bool, len(left)+1)
	for row := range cells {
		cells[row] = make([]map[state]bool, len(right)+1)
	}

	add := func(row int, column int, value state) {
		if value.run > max(0, opts.MaxLineDistance) {
			return
		}

		if cells[row][column] == nil {
			cells[row][column] = map[state]bool{}
		}

		cells[row][column][value] = true
	}
	add(0, 0, state{})

	for row := range cells {
		for column := range cells[row] {
			for value := range cells[row][column] {
				edit := state{run: value.run + 1, edited: true}
				if row < len(left) {
					add(row+1, column, edit)
				}

				if column < len(right) {
					add(row, column+1, edit)
				}

				if row < len(left) && column < len(right) {
					if left[row].text == right[column].text && (left[row].flags|right[column].flags)&matchesAlwaysDifferentLineFlag == 0 {
						add(row+1, column+1, state{edited: value.edited})
					} else {
						add(row+1, column+1, edit)
					}
				}
			}
		}
	}

	ends, level := [2]int{1, 1}, EqualSimilarityLevel

	for row := 1; row <= len(left); row++ {
		best, selectedColumn, bestLevel := max(0, opts.MaxLineDistance)+1, 0, differentSimilarityLevel

		for column, states := range cells[row] {
			for value := range states {
				candidateLevel := EqualSimilarityLevel
				if value.edited {
					candidateLevel = SimilarSimilarityLevel
				}

				if value.run < best || value.run == best && (candidateLevel > bestLevel || candidateLevel == bestLevel && column > selectedColumn) {
					best, selectedColumn, bestLevel = value.run, column, candidateLevel
				}
			}
		}

		if best > max(0, opts.MaxLineDistance) {
			break
		}

		ends, level = [2]int{row + 1, selectedColumn + 1}, bestLevel
	}

	return ends, level
}

func TestFileSimilaritiesLineEdits(t *testing.T) {
	lines := []string{"seed seed seed", "aaaaaaaaaaaa", "bbbbbbbbbbbb", "cccccccccccc", "dddddddddddd", "eeeeeeeeeeee", "ffffffffffff", "gggggggggggg", "hhhhhhhhhhhh", "iiiiiiiiiiii", "jjjjjjjjjjjj"}
	peer := append([]string(nil), lines[:3]...)
	peer = append(peer, "inserted inserted")
	peer = append(peer, lines[3:]...)

	for _, budget := range []int{0, 1, 3} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			opts := &Options{MinSimilarLines: 10, MaxLineDistance: budget}
			files := []*File{lineEditOccurrence(t, "alpha", lines, opts).File, lineEditOccurrence(t, "beta", peer, opts).File}
			search := newLineSearch(files, opts)
			indexed := &fileToCheck{f: files[0], inputs: files, search: search}
			linear := &fileToCheck{f: files[0], inputs: files}
			got := fileSimilarities(t.Context(), indexed, opts)

			want := fileSimilarities(t.Context(), linear, opts)
			if !reflect.DeepEqual(scanRecords(got), scanRecords(want)) {
				t.Fatal("indexed scan rejected an edited occurrence")
			}

			if budget > 0 && (len(got) != 1 || got[0].Occurrences[0].End != 11 || got[0].Occurrences[1].End != 12) {
				t.Fatalf("failed to report the edited block: %v", scanRecords(got))
			}

			if budget == 0 && len(got) != 0 {
				t.Fatal("strict mode reported alpha block shorter than the minimum")
			}
		})
	}
}

func TestSimilaritiesLineEdits(t *testing.T) {
	lines := []string{"seed seed seed", "aaaaaaaaaaaa", "bbbbbbbbbbbb", "cccccccccccc", "dddddddddddd", "eeeeeeeeeeee", "ffffffffffff", "gggggggggggg", "hhhhhhhhhhhh", "iiiiiiiiiiii", "jjjjjjjjjjjj"}
	peer := append([]string(nil), lines[:3]...)
	peer = append(peer, "inserted inserted")
	peer = append(peer, lines[3:]...)
	files := []*File{newFile("first", strings.Join(lines, "\n")), newFile("second", strings.Join(peer, "\n"))}
	opts := &Options{MinSimilarLines: 10, MaxLineDistance: 1}

	sims, progress, err := Similarities(t.Context(), files, opts)
	if err != nil {
		t.Fatal(err)
	}

	var findings []*Similarity

	waitForAll(drainProgressChan(progress), func() {
		findings = readSimilaritiesChan(sims)
	})

	if len(findings) != 1 || findings[0].Level != SimilarSimilarityLevel {
		t.Fatalf("edited block was filtered out: %v", findings)
	}

	if findings[0].Occurrences[0].End != 11 || findings[0].Occurrences[1].End != 12 {
		t.Fatal("public scan did not align the insertion")
	}
}

func TestLineEditShortTailBoundary(t *testing.T) {
	var source []string
	for letter := 'A'; letter < 'K'; letter++ {
		source = append(source, strings.Repeat(string(letter), 12))
	}

	for _, peerLines := range []int{9, 10} {
		t.Run(fmt.Sprint(peerLines), func(t *testing.T) {
			files := []*File{newFile("first", strings.Join(source, "\n")), newFile("second", strings.Join(source[:peerLines], "\n"))}
			opts := &Options{MinSimilarLines: 10, MaxLineDistance: 3}

			sims, progress, err := Similarities(t.Context(), files, opts)
			if err != nil {
				t.Fatal(err)
			}

			var findings []*Similarity

			waitForAll(drainProgressChan(progress), func() { findings = readSimilaritiesChan(sims) })

			if (len(findings) != 0) != (peerLines == 10) {
				t.Fatal("minLines was not enforced for every occurrence")
			}
		})
	}
}

func TestLineEditResetsAllowAccumulatedOffsets(t *testing.T) {
	for _, reversed := range []bool{false, true} {
		t.Run(fmt.Sprint(reversed), func(t *testing.T) {
			short := []string{"seed"}
			long := []string{"seed"}

			for index := range 40 {
				anchor := fmt.Sprintf("exact anchor %d", index)
				short = append(short, anchor)
				long = append(long, "inserted", anchor)
			}

			if reversed {
				short, long = long, short
			}

			opts := &Options{MaxLineDistance: 1}
			occs := []*FileOccurrence{lineEditOccurrence(t, "source", short, opts), lineEditOccurrence(t, "peer", long, opts)}

			level := expandOccurrences(t.Context(), occs, EqualSimilarityLevel, opts)
			if level != SimilarSimilarityLevel || occs[0].End != len(short) || occs[1].End != len(long) {
				t.Fatal("a fixed diagonal band or total edit budget truncated repeated resets")
			}
		})
	}
}

func TestIgnoredLinesDoNotResetEdits(t *testing.T) {
	opts := &Options{MaxLineDistance: 1, IgnoreLineRegex: regexp.MustCompile("^ignored$")}
	occs := []*FileOccurrence{
		lineEditOccurrence(t, "source", []string{"seed", "first", "ignored", "second", "tail"}, opts),
		lineEditOccurrence(t, "peer", []string{"seed", "First", "ignored", "Second", "tail"}, opts),
	}
	expandOccurrences(t.Context(), occs, EqualSimilarityLevel, opts)

	if occs[0].End != 2 || occs[1].End != 2 {
		t.Fatal("an ignored line reset the counter between two one-character changes")
	}
}

func TestAlwaysDifferentLinesDoNotResetEdits(t *testing.T) {
	opts := &Options{MaxLineDistance: 1, AlwaysDifferentLineRegex: regexp.MustCompile("^forced$")}
	lines := []string{"seed", "forced", "forced", "tail"}
	occs := []*FileOccurrence{lineEditOccurrence(t, "source", lines, opts), lineEditOccurrence(t, "peer", lines, opts)}
	expandOccurrences(t.Context(), occs, EqualSimilarityLevel, opts)

	if occs[0].End != 2 || occs[1].End != 2 {
		t.Fatal("identical always-different lines reset the counter")
	}
}

func TestMinLinesForEveryReportedGroup(t *testing.T) {
	opts := &Options{MinSimilarLines: 3, MaxLineDistance: 1}
	files := []*File{
		newFile("long-a", "alpha\nbeta\ngamma\ndelta"),
		newFile("long-b", "alpha\nbeta\ngamma\ndelta"),
		newFile("short-a", "first\nsecond"),
		newFile("short-b", "first\nsecond"),
	}

	sims, progress, err := Similarities(t.Context(), files, opts)
	if err != nil {
		t.Fatal(err)
	}

	var findings []*Similarity

	waitForAll(drainProgressChan(progress), func() { findings = readSimilaritiesChan(sims) })

	if len(findings) != 1 {
		t.Fatalf("got %d groups, want only the group meeting minLines", len(findings))
	}

	for _, finding := range findings {
		for _, occurrence := range finding.Occurrences {
			if occurrence.End-occurrence.Start < opts.MinSimilarLines {
				t.Fatal("reported an occurrence shorter than minLines")
			}
		}
	}
}
