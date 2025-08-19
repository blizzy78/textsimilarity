package textsimilarity

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"
)

func FuzzFileSimilarities(f *testing.F) { //nolint:gocognit,cyclop // test code
	const (
		maxLines   = 100
		maxLineLen = 50
	)

	f.Add("line1\nline2\nline3\nline4\n𨊂 € 🚀", 2, 0, 0, false, false)
	f.Add("", 0, 0, 0, false, false)
	f.Add("single line", 1, 0, 0, false, false)
	f.Add("identical\ncontent\nfor\nboth", 0, 4, 0, false, false)
	f.Add("a\nb\nc\nd\ne\nf", 3, 0, 0, false, false)
	f.Add("hello world\ntest line\nanother test", 1, 0, 0, false, false)
	f.Add("aaaaa\nbbbbb\nccccc\naaaaa\nbbbbb\nccccc", 3, 0, 0, false, false)
	f.Add("aaaaa\n   bbbbb   \nccccc\n aaaaa \nbbbbb\nccccc", 3, 0, 0, true, false)
	f.Add("aaaaa\nbbbbb\n\nccccc\naaaaa\nbbbbb\nccccc", 3, 0, 0, false, true)
	f.Add("aaaaa\n   bbbbb   \nccccc\n aaaaa \n\nbbbbb\nccccc", 3, 0, 2, true, true)

	f.Fuzz(func(t *testing.T, content string, splitIdx int, minLineLength int, minSimilarLines int, ignoreWS bool, ignoreBlankLines bool) {
		if splitIdx < -1 {
			t.SkipNow()
		}

		if minLineLength < 0 || minLineLength > maxLineLen {
			t.SkipNow()
		}

		if minSimilarLines < 0 || minSimilarLines > maxLines {
			t.SkipNow()
		}

		lines := strings.Split(content, "\n")
		if len(lines) > maxLines {
			t.SkipNow()
		}

		if splitIdx > len(lines) {
			t.SkipNow()
		}

		for idx, line := range lines {
			runes := []rune(line)
			if len(runes) <= maxLineLen {
				continue
			}

			lines[idx] = string(runes[:maxLineLen])
		}

		var (
			lines1 []string
			lines2 []string
		)

		if splitIdx < 0 {
			lines1 = lines
			lines2 = lines
		} else {
			lines1 = lines[:splitIdx]
			lines2 = lines[splitIdx:]
		}

		file1 := &File{Name: "file1.txt"}
		file2 := &File{Name: "file2.txt"}

		done1 := make([]bool, len(lines1))
		done2 := make([]bool, len(lines2))

		fileToCheck1 := newFileToCheck(t, lines1, done1, false)
		fileToCheck2 := newFileToCheck(t, lines2, done2, false)

		fileToCheck1.f = file1
		fileToCheck2.f = file2

		fileToCheck1.peers = []*fileToCheck{fileToCheck1, fileToCheck2}

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		opts := &Options{
			MaxEditDistance: 2,
			MinLineLength:   minLineLength,
			MinSimilarLines: minSimilarLines,
		}

		if ignoreWS {
			opts.Flags |= IgnoreWhitespaceFlag
		}

		if ignoreBlankLines {
			opts.Flags |= IgnoreBlankLinesFlag
		}

		similarities := fileSimilarities(ctx, fileToCheck1, opts)

		for _, sim := range similarities {
			if sim.Level != EqualSimilarityLevel && sim.Level != SimilarSimilarityLevel {
				t.Errorf("Invalid similarity level: %v", sim.Level)
				continue
			}

			if len(sim.Occurrences) < 2 {
				t.Errorf("Similarity has less than 2 occurrences: %d", len(sim.Occurrences))
				continue
			}

			for _, occ := range sim.Occurrences {
				if occ.File == nil {
					t.Errorf("Occurrence has nil File reference")
					continue
				}

				var maxLines int

				switch occ.File {
				case file1:
					maxLines = len(lines1)
				case file2:
					maxLines = len(lines2)
				default:
					t.Errorf("Occurrence references unknown file")
					continue
				}

				if occ.Start < 0 {
					t.Errorf("Occurrence Start is negative: %d", occ.Start)
					continue
				}

				if occ.End <= occ.Start {
					t.Errorf("Occurrence End (%d) is not greater than Start (%d)", occ.End, occ.Start)
					continue
				}

				if occ.End > maxLines {
					t.Errorf("Occurrence End (%d) exceeds file line count (%d)", occ.End, maxLines)
					continue
				}
			}

			sameFileOccs := make([]*FileOccurrence, len(sim.Occurrences))
			for _, occ := range sim.Occurrences {
				if occ.fileToCheck.f != fileToCheck1.f {
					continue
				}

				sameFileOccs = append(sameFileOccs, occ)
			}

			if len(sameFileOccs) >= 2 {
				sort.Slice(sameFileOccs, func(i int, j int) bool {
					return sameFileOccs[i].Start < sameFileOccs[j].Start
				})

				for i := 1; i < len(sameFileOccs); i++ {
					if sameFileOccs[i].Start < sameFileOccs[i-1].End {
						t.Errorf("Overlapping occurrences found: %v and %v", sameFileOccs[i], sameFileOccs[i-1])
						continue
					}
				}
			}
		}
	})
}
