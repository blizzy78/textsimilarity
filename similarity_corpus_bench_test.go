package textsimilarity

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// BenchmarkSimilaritiesCorpus checks every Java file directly in the directory
// selected by TEXTSIMILARITY_CORPUS, with the CLI's default similarity options.
func BenchmarkSimilaritiesCorpus(b *testing.B) { //nolint:gocognit,cyclop // external corpus loading and complete API measurement
	dir := os.Getenv("TEXTSIMILARITY_CORPUS")
	if dir == "" {
		b.Skip("set TEXTSIMILARITY_CORPUS to a Java source package")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		b.Fatal(err)
	}

	var names, texts []string

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".java" {
			continue
		}

		name := filepath.Join(dir, entry.Name())

		data, err := os.ReadFile(name) //nolint:gosec // the benchmark intentionally reads the caller-selected corpus
		if err != nil {
			b.Fatal(err)
		}

		names = append(names, name)
		texts = append(texts, string(data))
	}

	if len(names) == 0 {
		b.Fatal("no Java source files")
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
