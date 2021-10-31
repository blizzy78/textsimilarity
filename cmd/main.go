package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/template"
	"time"

	"github.com/blizzy78/textsimilarity"
	tsio "github.com/blizzy78/textsimilarity/internal/io"
)

const (
	// clearLine is the ANSI escape sequence to clear the current line.
	clearLine = "\033[2K"

	// moveUp is the ANSI escape sequence to move cursor to the beginning of the previous line.
	moveUp = "\033[F"
)

// cmdOptions holds command line options.
type cmdOptions struct {
	// showProgress indicates whether progress should be written to stderr.
	showProgress bool

	// printEqual indicates whether exactly equal similarities should be printed.
	printEqual bool

	// diffTool is a command line template for a diff tool to print similar, but not exactly equal, similarities.
	diffTool *template.Template

	// simOpts specifies options for similarity calculations.
	simOpts textsimilarity.Options
}

// errCanceled is returned when the context is canceled.
var errCanceled = errors.New("")

func main() {
	opts, err := options()
	if err != nil {
		panic(err)
	}

	if err := run(flag.Args(), opts); err != nil {
		if errors.Is(err, errCanceled) {
			if opts.showProgress {
				fmt.Fprint(os.Stderr, "Canceled.\n")
			}

			os.Exit(1)
		}

		panic(err)
	}
}

// options parses and returns the command line options.
func options() (cmdOptions, error) {
	showProgress := false
	printEqual := false
	diffTool := ""

	ignoreWhitespace := false
	ignoreBlankLines := false
	minLineLength := 0
	minSimilarLines := 10
	maxEditDistance := textsimilarity.DefaultMaxEditDistance
	ignoreLineRegex := ""

	flag.BoolVar(&showProgress, "progress", showProgress, "write progress to stderr")
	flag.BoolVar(&printEqual, "printEqual", printEqual, "print equal similarities")
	flag.StringVar(&diffTool, "diffTool", diffTool, "diff tool command line template")

	flag.BoolVar(&ignoreWhitespace, "ignoreWS", ignoreWhitespace, "ignore whitespace")
	flag.BoolVar(&ignoreBlankLines, "ignoreBlank", ignoreBlankLines, "ignore blank lines")
	flag.IntVar(&minLineLength, "minLen", minLineLength, "minimum line length")
	flag.IntVar(&minSimilarLines, "minLines", minSimilarLines, "minimum similar lines")
	flag.IntVar(&maxEditDistance, "maxDist", maxEditDistance, "maximum edit distance")
	flag.StringVar(&ignoreLineRegex, "ignoreRE", ignoreLineRegex, "ignore lines matching regex")

	flag.Parse()

	simOpts := textsimilarity.Options{
		MinLineLength:   minLineLength,
		MinSimilarLines: minSimilarLines,
		MaxEditDistance: maxEditDistance,
	}

	if ignoreWhitespace {
		simOpts.Flags |= textsimilarity.IgnoreWhitespaceFlag
	}

	if ignoreBlankLines {
		simOpts.Flags |= textsimilarity.IgnoreBlankLinesFlag
	}

	if ignoreLineRegex != "" {
		simOpts.IgnoreLineRegex = regexp.MustCompile(ignoreLineRegex)
	}

	cmdOpts := cmdOptions{
		showProgress: showProgress,
		printEqual:   printEqual,

		simOpts: simOpts,
	}

	if diffTool != "" {
		var err error
		cmdOpts.diffTool, err = template.New("diffTool").Parse(diffTool)

		if err != nil {
			return cmdOptions{}, fmt.Errorf("parse diff tool template: %w", err)
		}
	}

	return cmdOpts, nil
}

func run(paths []string, opts cmdOptions) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	progress := func(prog textsimilarity.Progress) {
		if !opts.showProgress {
			return
		}

		fmt.Fprintf(os.Stderr, "\n"+clearLine+"%s"+moveUp+clearLine+"%.1f%%, ETA: %s   ", prog.File.Name, prog.Done, prog.ETA.Local().Format(time.Kitchen))
	}

	sims, err := similarities(ctx, paths, opts.simOpts, progress)
	if err != nil {
		return err
	}

	if opts.showProgress {
		fmt.Fprint(os.Stderr, clearLine+"\n"+clearLine+moveUp)
	}

	if contextDone(ctx) {
		return errCanceled
	}

	sortSimilaritiesLines(sims)

	return printSimilarities(ctx, sims, opts)
}

// printSimilarities prints occurrences in sims. If opts.diffTool is set, it will run it to show differences.
func printSimilarities(ctx context.Context, sims []*textsimilarity.Similarity, opts cmdOptions) error {
	for idx, sim := range sims {
		if contextDone(ctx) {
			return errCanceled
		}

		level := "exactly equal"
		if sim.Level == textsimilarity.SimilarSimilarityLevel {
			level = "similar"
		}

		if idx > 0 {
			fmt.Println()
		}

		fmt.Printf("similarity #%d - %d lines, %s\n", idx+1, sim.Occurrences[0].End-sim.Occurrences[0].Start, level)

		for _, occ := range sim.Occurrences {
			fmt.Printf("- %s: ", occ.File.Name)

			if occ.End == occ.Start+1 {
				fmt.Print(strconv.Itoa(occ.Start + 1))
			} else {
				fmt.Printf("%d-%d", occ.Start+1, occ.End)
			}

			fmt.Println()
		}

		if err := dumpOrDiff(ctx, sim, opts); err != nil {
			return err
		}
	}

	return nil
}

// dumpOrDiff prints sim's text:
// If sim.Level==textsimilarity.EqualSimilarityLevel and opts.printEqual==true, it will dump the first occurrence's text.
// If sim.Level==textsimilarity.SimilarSimilarityLevel and opts.diffTool!="", it will run opts.diffTool to print differences.
func dumpOrDiff(ctx context.Context, sim *textsimilarity.Similarity, opts cmdOptions) error {
	switch {
	case sim.Level == textsimilarity.EqualSimilarityLevel && opts.printEqual:
		fmt.Println("\n------------------------------")

		if err := dump(sim.Occurrences[0]); err != nil {
			return err
		}

		fmt.Println("------------------------------")

	case sim.Level == textsimilarity.SimilarSimilarityLevel && opts.diffTool != nil:
		fmt.Println("\n------------------------------")

		if err := diff(ctx, sim, opts); err != nil {
			return err
		}

		fmt.Println("------------------------------")
	}

	return nil
}

// dump prints the text of occ.
func dump(occ *textsimilarity.FileOccurrence) error {
	text, err := fileText(occ.File.Name, occ.Start, occ.End)
	if err != nil {
		return err
	}

	fmt.Print(text)

	return nil
}

// diff uses opts.diffTool to print differences between occurrences in sim.
func diff(ctx context.Context, sim *textsimilarity.Similarity, opts cmdOptions) error {
	text1, err := fileText(sim.Occurrences[0].File.Name, sim.Occurrences[0].Start, sim.Occurrences[0].End)
	if err != nil {
		return err
	}

	path1, err := writeTempFile(text1)
	if err != nil {
		return err
	}

	defer os.Remove(path1)

	var text2 string

	// get text of an occurrence that is not exactly equal to sim.Occurrences[0]
	for _, occ := range sim.Occurrences[1:] {
		text2, err = fileText(occ.File.Name, occ.Start, occ.End)
		if err != nil {
			return err
		}

		if text2 == text1 {
			continue
		}

		break
	}

	path2, err := writeTempFile(text2)
	if err != nil {
		return err
	}

	defer os.Remove(path2)

	return runDiffTool(ctx, path1, path2, opts)
}

// runDiffTool runs opts.diffTool to print differences between files path1 and path2.
func runDiffTool(ctx context.Context, path1 string, path2 string, opts cmdOptions) error {
	buf := strings.Builder{}

	err := opts.diffTool.Execute(&buf, struct {
		File1 string
		File2 string
	}{
		File1: path1,
		File2: path2,
	})

	if err != nil {
		return fmt.Errorf("construct diff tool command line: %w", err)
	}

	parts := strings.Split(buf.String(), " ")

	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...) //nolint:gosec // okay

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("run diff tool: %w", err)
	}

	fmt.Print(string(output))

	return nil
}

// writeTempFile writes text to a temporary file and returns its path.
func writeTempFile(text string) (string, error) {
	file, err := os.CreateTemp("", "similarity")
	if err != nil {
		return "", fmt.Errorf("create: %w", err)
	}
	defer file.Close()

	if _, err = file.WriteString(text); err != nil {
		return "", fmt.Errorf("write: %w", err)
	}

	return file.Name(), nil
}

// fileText returns the text of file path, starting from startLine (zero-based), up to endLine (zero-based, exclusive.)
func fileText(path string, startLine int, endLine int) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open: %w", err)
	}
	defer file.Close()

	textBuf := strings.Builder{}

	reader := bufio.NewReader(file)
	buf := bytes.Buffer{}

	for lineIdx := 0; lineIdx < endLine; lineIdx++ {
		line, err := tsio.ReadLine(reader, &buf)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return "", fmt.Errorf("read line: %w", err)
		}

		if lineIdx < startLine {
			continue
		}

		textBuf.WriteString(line)
		textBuf.WriteString("\n")
	}

	return textBuf.String(), nil
}

// similarities calculates similarities between files in paths, according to opts. Progress is reported to progress.
func similarities(ctx context.Context, paths []string, opts textsimilarity.Options, progress func(textsimilarity.Progress)) ([]*textsimilarity.Similarity, error) {
	var osFiles []*os.File

	defer func() {
		for _, f := range osFiles {
			_ = f.Close()
		}
	}()

	files, osFiles, err := openFiles(ctx, paths)
	if err != nil {
		return nil, err
	}

	if contextDone(ctx) {
		return nil, nil
	}

	simsCh, progressCh, err := textsimilarity.Similarities(ctx, files, &opts)
	if err != nil {
		return nil, err
	}

	grp := sync.WaitGroup{}
	grp.Add(2)

	go func() {
		defer grp.Done()

		for p := range progressCh {
			progress(p)
		}
	}()

	sims := []*textsimilarity.Similarity{}

	go func() {
		defer grp.Done()

		for sim := range simsCh {
			sims = append(sims, sim)
		}
	}()

	grp.Wait()

	return sims, nil
}

// openFiles opens files in paths and returns corresponding slices of textsimilarity.File and os.File.
// The returned os.Files must be closed by the caller. If an error occurs, the os.Files opened so far
// will be returned and must be closed by the caller.
func openFiles(ctx context.Context, paths []string) ([]*textsimilarity.File, []*os.File, error) {
	files := []*textsimilarity.File{}
	osFiles := []*os.File{}

	for _, path := range paths {
		if contextDone(ctx) {
			return nil, osFiles, nil
		}

		osFile, err := os.Open(path)
		if err != nil {
			return nil, osFiles, fmt.Errorf("open %s: %w", path, err)
		}

		osFiles = append(osFiles, osFile)

		files = append(files, &textsimilarity.File{
			Name: path,
			R:    osFile,
		})
	}

	return files, osFiles, nil
}

// sortSimilaritiesLines sorts sims by number of lines, in reverse order.
func sortSimilaritiesLines(sims []*textsimilarity.Similarity) {
	sort.SliceStable(sims, func(a int, b int) bool {
		lines1 := similarityLines(sims[a])
		lines2 := similarityLines(sims[b])

		// reverse
		return lines1 > lines2
	})
}

// similarityLines returns the number of lines of all occurrences in sim.
func similarityLines(sim *textsimilarity.Similarity) int {
	lines := 0
	for _, occ := range sim.Occurrences {
		lines += occ.End - occ.Start
	}

	return lines
}

// contextDone returns whether ctx is done.
func contextDone(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}
