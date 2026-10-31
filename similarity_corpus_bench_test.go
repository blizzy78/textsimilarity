package textsimilarity

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// BenchmarkSimilaritiesCorpus checks every file recursively in the directory
// selected by TEXTSIMILARITY_CORPUS, with the CLI's default similarity options.
func BenchmarkSimilaritiesCorpus(b *testing.B) { //nolint:gocognit,cyclop // external corpus loading and complete API measurement
	dir := os.Getenv("TEXTSIMILARITY_CORPUS")
	if dir == "" {
		b.Skip("set TEXTSIMILARITY_CORPUS to a text corpus directory")
	}

	var names, texts []string

	err := filepath.WalkDir(dir, func(name string, entry os.DirEntry, walkErr error) error { //nolint:gosec // the benchmark intentionally walks the caller-selected corpus
		if walkErr != nil {
			return walkErr
		}

		if entry.IsDir() {
			return nil
		}

		data, err := os.ReadFile(name) //nolint:gosec // the benchmark intentionally reads the caller-selected corpus
		if err != nil {
			return fmt.Errorf("read corpus file: %w", err)
		}

		names = append(names, name)
		texts = append(texts, string(data))

		return nil
	})
	if err != nil {
		b.Fatal(err)
	}

	if len(names) == 0 {
		b.Fatal("no corpus files")
	}

	opts := &Options{MinSimilarLines: 10}

	b.ResetTimer()

	for b.Loop() {
		files := make([]*File, len(names))
		for idx, name := range names {
			files[idx] = &File{Name: name, R: strings.NewReader(texts[idx])}
		}

		sims, progress, err := Similarities(context.Background(), files, opts)
		if err != nil {
			b.Fatal(err)
		}

		done := make(chan struct{})

		go func() {
			for range progress {
			}

			close(done)
		}()

		count := 0
		for range sims {
			count++
		}

		<-done

		similarityCount = count
	}
}
