package textsimilarity

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	tsio "github.com/blizzy78/textsimilarity/internal/io"
)

func TestFileLoadingMatchesBufferedLines(t *testing.T) { //nolint:gocognit // compare the complete loading contract with the previous line reader
	for _, text := range []string{"", "\n", "\r\n", "a\r", "a\r\n", "a\n\n", "a\r\r\n", "\x00\xff\n🚀\n", strings.Repeat("é", 10000) + "\nlast"} {
		file := &File{R: strings.NewReader(text)}
		if err := file.load(&Options{}); err != nil {
			t.Fatal(err)
		}

		reader := bufio.NewReader(strings.NewReader(text))

		var (
			buffer   bytes.Buffer
			expected []string
		)

		for {
			line, err := tsio.ReadLine(reader, &buffer)
			if errors.Is(err, io.EOF) {
				break
			}

			if err != nil {
				t.Fatal(err)
			}

			expected = append(expected, line)
		}

		if len(file.lines) != len(expected) {
			t.Fatalf("input %q: got %d lines, want %d", text, len(file.lines), len(expected))
		}

		for idx, want := range expected {
			if file.lines[idx].text != want {
				t.Fatalf("line %d differs", idx)
			}
		}
	}
}

type emptyReader struct{}

func (emptyReader) Read([]byte) (int, error) { return 0, nil }

func TestFileLoadingNoProgress(t *testing.T) {
	file := &File{R: emptyReader{}}
	if err := file.load(&Options{}); !errors.Is(err, io.ErrNoProgress) {
		t.Fatalf("got %v, want ErrNoProgress", err)
	}
}
