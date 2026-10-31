package levenshtein

import (
	"math/rand/v2"
	"strings"
	"testing"
)

func TestWithinDistance(t *testing.T) { //nolint:gocognit,cyclop // differential checks include random mutations and boundary cases
	tests := [][2]string{
		{"", ""}, {"", "abc"}, {"kitten", "sitting"},
		{"prefix-middle-suffix", "prefix-Xmiddle-suffix"},
		{"ab", "ba"}, {"abc", "xyz"}, {"aaaaab", "baaaaa"},
		{"𨊂 € 🚀", "𨊂 € 🌍"}, {"é", "e\u0301"},
		{strings.Repeat("a", 300), strings.Repeat("b", 300)},
		{strings.Repeat("ab", 150), strings.Repeat("ba", 150)},
	}
	for _, pair := range tests {
		checkWithinDistance(t, []rune(pair[0]), []rune(pair[1]))
	}

	random := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // deterministic test inputs, not secrets
	alphabet := []rune("abcé€𨊂🚀")

	for range 2000 {
		left := make([]rune, random.IntN(150))

		right := make([]rune, random.IntN(150))
		for idx := range left {
			left[idx] = alphabet[random.IntN(len(alphabet))]
		}

		for idx := range right {
			right[idx] = alphabet[random.IntN(len(alphabet))]
		}

		checkWithinDistance(t, left, right)

		// Pairs near the cutoff exercise successful paths through the band,
		// including insertions and deletions that move away from the diagonal.
		right = append(right[:0], left...)
		for range random.IntN(8) {
			pos := random.IntN(len(right) + 1)
			switch random.IntN(3) {
			case 0:
				right = append(right[:pos], append([]rune{alphabet[random.IntN(len(alphabet))]}, right[pos:]...)...)
			case 1:
				if pos < len(right) {
					right = append(right[:pos], right[pos+1:]...)
				}
			case 2:
				if pos < len(right) {
					right[pos] = alphabet[random.IntN(len(alphabet))]
				}
			}
		}

		checkWithinDistance(t, left, right)
	}
}

func checkWithinDistance(t *testing.T, left []rune, right []rune) {
	t.Helper()

	distance := referenceDistance(left, right)
	for _, limit := range []int{-1, 0, 1, 2, 5, 64, distance - 1, distance, distance + 1, int(^uint(0) >> 1)} {
		want := distance <= limit
		if got := WithinDistance(left, right, limit); got != want {
			t.Fatalf("WithinDistance(%q, %q, %d) = %t; distance = %d", string(left), string(right), limit, got, distance)
		}

		if got := WithinDistance(right, left, limit); got != want {
			t.Fatalf("WithinDistance(%q, %q, %d) = %t; distance = %d", string(right), string(left), limit, got, distance)
		}
	}
}

// referenceDistance deliberately uses the full matrix, independently of the
// bounded implementation, to catch incorrect pruning and band boundaries.
func referenceDistance(left []rune, right []rune) int {
	matrix := make([][]int, len(left)+1)
	for idx := range matrix {
		matrix[idx] = make([]int, len(right)+1)
		matrix[idx][0] = idx
	}

	for idx := range matrix[0] {
		matrix[0][idx] = idx
	}

	for row, leftRune := range left {
		for col, rightRune := range right {
			cost := 0
			if leftRune != rightRune {
				cost = 1
			}

			matrix[row+1][col+1] = min(matrix[row][col]+cost, min(matrix[row][col+1]+1, matrix[row+1][col]+1))
		}
	}

	return matrix[len(left)][len(right)]
}

func FuzzWithinDistance(f *testing.F) {
	f.Add("kitten", "sitting")
	f.Add("prefix𨊂suffix", "prefix🚀suffix")
	f.Fuzz(func(t *testing.T, left string, right string) {
		if len(left) > 512 || len(right) > 512 {
			t.Skip()
		}

		checkWithinDistance(t, []rune(left), []rune(right))
	})
}
