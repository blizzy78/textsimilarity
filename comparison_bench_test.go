package textsimilarity

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// BenchmarkComparisonsCorpus excludes file reading and parsing. All candidate
// indexing, matching, deduplication and channel delivery remain inside the timer.
// Inputs are loaded again before each scan, so no prepared headers or index is reused.
func BenchmarkComparisonsCorpus(b *testing.B) {
	names, texts := readComparisonCorpus(b)
	opts := &Options{MinSimilarLines: 10, MaxLineDistance: DefaultMaxLineDistance}

	if raw := os.Getenv("TEXTSIMILARITY_MAX_LINE_DIST"); raw != "" {
		distance, err := strconv.Atoi(raw)
		if err != nil || distance < 0 {
			b.Fatal("TEXTSIMILARITY_MAX_LINE_DIST must be a nonnegative integer")
		}

		opts.MaxLineDistance = distance
	}

	b.ResetTimer()

	for idx := 0; idx < b.N; idx++ {
		b.StopTimer()
		files := loadComparisonCorpus(b, names, texts, opts)
		b.StartTimer()

		sims, progress := similaritiesLoaded(context.Background(), files, opts)
		similarityCount = countBenchmarkSimilarities(sims, progress)
	}
}

func comparisonCorpusNames(tb testing.TB) []string {
	tb.Helper()

	dir := os.Getenv("TEXTSIMILARITY_CORPUS")

	manifest := os.Getenv("TEXTSIMILARITY_INPUTS")
	if dir == "" && manifest == "" {
		tb.Skip("set TEXTSIMILARITY_CORPUS to a directory or TEXTSIMILARITY_INPUTS to a NUL-separated file list")
	}

	if manifest != "" {
		data, err := os.ReadFile(manifest) //nolint:gosec // explicit benchmark input list
		if err != nil {
			tb.Fatal(err)
		}

		return strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
	}

	var names []string

	err := filepath.WalkDir(dir, func(name string, entry os.DirEntry, walkErr error) error { //nolint:gosec // bounded packed values or explicitly selected benchmark input
		if walkErr != nil {
			return walkErr
		}

		if !entry.IsDir() {
			names = append(names, name)
		}

		return nil
	})
	if err != nil {
		tb.Fatal(err)
	}

	return names
}

func readComparisonCorpus(tb testing.TB) ([]string, []string) {
	tb.Helper()

	names := comparisonCorpusNames(tb)
	if len(names) == 0 {
		tb.Fatal("no corpus files")
	}

	texts := make([]string, len(names))
	for idx, name := range names {
		data, err := os.ReadFile(name)
		if err != nil {
			tb.Fatal(err)
		}

		texts[idx] = string(data)
	}

	return names, texts
}

func loadComparisonCorpus(tb testing.TB, names []string, texts []string, opts *Options) []*File {
	tb.Helper()

	buffer := make([]byte, 32*1024)

	files := make([]*File, len(names))
	for idx, name := range names {
		files[idx] = &File{Name: name, R: strings.NewReader(texts[idx])}
		if err := files[idx].loadBuffered(opts, buffer); err != nil {
			tb.Fatal(err)
		}
	}

	return files
}
