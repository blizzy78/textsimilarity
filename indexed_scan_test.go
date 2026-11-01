package textsimilarity

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// Compare each root before concurrent deduplication, so scheduling cannot hide
// differences in levels, ranges, ordering, or root-local marker updates.
func TestIndexedScanMatchesLinearScan(t *testing.T) { //nolint:gocognit,cyclop // randomized differential corpus covers independent combinations
	random := rand.New(rand.NewPCG(93, 94)) //nolint:gosec // reproducible test corpus
	pool := []string{"", "}", "alpha beta", "alpha betb", "func main() {}", "const n = 1;", "# Heading", "Ordinary prose", "日本語", "é é 🚀", "\xff\xfe", strings.Repeat("a", 255), strings.Repeat("a", 256)}

	for trial := 0; trial < 500; trial++ {
		opts := &Options{MinSimilarLines: 4 + random.IntN(9), MaxEditDistance: 1 + random.IntN(8)}
		if trial%3 == 0 {
			opts.Flags = IgnoreWhitespaceFlag
		}

		if trial%7 == 0 {
			opts.AlwaysDifferentLineRegex = regexp.MustCompile("Heading")
		}

		shared := make([]string, 15+random.IntN(20))
		for idx := range shared {
			shared[idx] = pool[random.IntN(len(pool))]
		}

		files := make([]*File, 2+random.IntN(4))
		for idx := range files {
			lines := append([]string(nil), shared...)
			for range random.IntN(8) {
				lines[random.IntN(len(lines))] = pool[random.IntN(len(pool))]
			}
			// Vary EOF and final newline independently of the shared blocks.
			lines = lines[:len(lines)-random.IntN(4)]

			text := strings.Join(lines, "\n")
			if random.IntN(2) == 0 {
				text += "\n"
			}

			files[idx] = newFile(fmt.Sprint(idx), text)
			if err := files[idx].loadBuffered(opts, blockWidth(opts), make([]byte, 4096)); err != nil {
				t.Fatal(err)
			}
		}

		search := newLineSearch(files, opts)
		blocks := newAnchorSearch(files, opts)
		search.blocks = blocks
		// Shared immutable indexes and query caches must also work concurrently.
		var roots sync.WaitGroup
		for idx, file := range files {
			roots.Go(func() {
				indexed := &fileToCheck{f: file, inputs: files, fileID: idx, search: search, blocks: blocks}
				linear := &fileToCheck{f: file, inputs: files, fileID: idx}
				got := scanRecords(fileSimilarities(t.Context(), indexed, opts))

				want := scanRecords(fileSimilarities(t.Context(), linear, opts))
				if !reflect.DeepEqual(got, want) {
					t.Errorf("trial %d root %d: indexed %v, linear %v", trial, idx, got, want)
				}
			})
		}

		roots.Wait()
	}
}

func scanRecords(sims []*Similarity) []string {
	records := make([]string, 0, len(sims))

	for _, sim := range sims {
		record := fmt.Sprint(sim.Level)
		for _, occ := range sim.Occurrences {
			record += fmt.Sprintf(" %s:%d:%d", occ.File.Name, occ.Start, occ.End)
		}

		records = append(records, record)
	}

	return records
}

func TestIndexedScanLongLines(t *testing.T) {
	for _, length := range []int{65500, 65504, 65505, 65536, 65537, 100000} {
		opts := &Options{MinSimilarLines: 4}
		text := strings.Repeat("x", length) + "\nalpha beta\ngamma delta\nepsilon zeta\n"

		files := []*File{newFile("a", text), newFile("b", text)}
		for _, file := range files {
			if err := file.loadBuffered(opts, blockWidth(opts), make([]byte, 4096)); err != nil {
				t.Fatal(err)
			}
		}

		search := newLineSearch(files, opts)
		blocks := newAnchorSearch(files, opts)
		search.blocks = blocks
		indexed := &fileToCheck{f: files[0], inputs: files, search: search, blocks: blocks}
		linear := &fileToCheck{f: files[0], inputs: files}
		got := scanRecords(fileSimilarities(t.Context(), indexed, opts))

		want := scanRecords(fileSimilarities(t.Context(), linear, opts))
		if len(got) != 1 || !reflect.DeepEqual(got, want) {
			t.Fatalf("length %d: indexed %v, linear %v", length, got, want)
		}
	}
}

func TestIndexedScanOptionBoundaries(t *testing.T) { //nolint:gocognit // compare both index bounds with a full matching block
	for _, span := range []int{4, 64} {
		for _, limit := range []int{1, 32} {
			opts := &Options{MinSimilarLines: span, MaxEditDistance: limit}

			lines := make([]string, 80)
			for idx := range lines {
				lines[idx] = strings.Repeat("x", 100+65*idx)
			}

			first := strings.Join(lines, "\n")
			lines[40] = "y" + lines[40][1:]

			files := []*File{newFile("a", first), newFile("b", strings.Join(lines, "\n"))}
			for _, file := range files {
				if err := file.loadBuffered(opts, blockWidth(opts), make([]byte, 4096)); err != nil {
					t.Fatal(err)
				}
			}

			search, blocks := newLineSearch(files, opts), newAnchorSearch(files, opts)
			search.blocks = blocks
			indexed := &fileToCheck{f: files[0], inputs: files, search: search, blocks: blocks}
			linear := &fileToCheck{f: files[0], inputs: files}
			got := scanRecords(fileSimilarities(t.Context(), indexed, opts))

			want := scanRecords(fileSimilarities(t.Context(), linear, opts))
			if len(got) != 1 || !reflect.DeepEqual(got, want) {
				t.Fatalf("span %d limit %d: indexed %v, linear %v", span, limit, got, want)
			}
		}
	}
}
