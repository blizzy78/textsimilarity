package levenshtein

import (
	"math/rand/v2"
	"strings"
	"testing"
)

func TestDistance(t *testing.T) {
	random := rand.New(rand.NewPCG(3, 4)) //nolint:gosec // deterministic test inputs, not secrets
	alphabet := []rune("abcé€")

	for range 1000 {
		left := make([]rune, random.IntN(180))

		right := make([]rune, random.IntN(180))
		for idx := range left {
			left[idx] = alphabet[random.IntN(len(alphabet))]
		}

		for idx := range right {
			right[idx] = alphabet[random.IntN(len(alphabet))]
		}

		// Equal ends must not hide a distance in the remaining middle.
		left = append([]rune("prefix"), append(left, []rune("suffix")...)...)
		right = append([]rune("prefix"), append(right, []rune("suffix")...)...)

		checkExactDistance(t, left, right)

		if got := Distance(left, left); got != 0 {
			t.Fatalf("Distance of equal inputs = %d, want 0", got)
		}
	}
}

func TestDistanceShortPatterns(t *testing.T) {
	for _, length := range []int{15, 16, 63, 64, 65} {
		for _, char := range []string{"a", "é", "🚀"} {
			pattern := strings.Repeat(char, length)
			for _, text := range []string{pattern, "\t" + pattern + ";", strings.Repeat("x", 130) + pattern, "🚀" + pattern + "€"} {
				left, right := []rune(pattern), []rune(text)
				// The multi-word exact implementation only accepts BMP runes.
				if length <= 64 || (char != "🚀" && !strings.ContainsRune(text, '🚀')) {
					checkExactDistance(t, left, right)
				}

				checkWithinDistance(t, left, right)
			}
		}
	}
}

func checkExactDistance(t *testing.T, left, right []rune) {
	t.Helper()

	want := referenceDistance(left, right)
	if got := Distance(left, right); got != want {
		t.Fatalf("Distance(%q, %q) = %d, want %d", string(left), string(right), got, want)
	}

	if got := Distance(right, left); got != want {
		t.Fatalf("Distance reversed = %d, want %d", got, want)
	}
}
