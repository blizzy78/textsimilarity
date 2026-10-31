package levenshtein

// WithinDistance reports whether the rune-based Levenshtein distance between left
// and right is at most limit. A negative limit never matches. Unlike Distance's
// bit-vector implementation, it supports all Unicode code points.
func WithinDistance(left []rune, right []rune, limit int) bool { //nolint:gocognit,cyclop // banded dynamic programming
	if limit < 0 {
		return false
	}

	if len(left) < len(right) {
		left, right = right, left
	}

	if len(left)-len(right) > limit {
		return false
	}

	if len(left) <= limit {
		return true
	}

	left, right = trimEqualEnds(left, right)
	if len(right) == 0 || len(left) <= limit {
		return len(left) <= limit
	}

	// A single word avoids the cost of visiting individual band cells. For
	// tiny patterns the bounded loop costs less than initializing a mask table.
	if len(right) >= 16 && len(right) <= 64 && limit >= 2 {
		return m64(right, left) <= limit
	}

	if useBitVector(left, right, limit) {
		return Distance(left, right) <= limit
	}

	// Most lines fit on the stack. Longer lines need only one row, rather than
	// a matrix proportional to the product of the line lengths.
	var buffer [128]int

	row := buffer[:]
	if len(right)+1 > len(row) {
		row = make([]int, len(right)+1)
	}

	for col := 0; col <= min(len(right), limit); col++ {
		row[col] = col
	}

	if limit < len(right) {
		row[limit+1] = limit + 1
	}

	for idx, char := range left {
		line := idx + 1
		start := max(1, line-limit)
		end := min(len(right), line+limit)
		diagonal := row[start-1]

		row[start-1] = limit + 1
		if start == 1 {
			row[0] = line
		}

		best := row[start-1]
		for col := start; col <= end; col++ {
			above := row[col]

			cost := 0
			if char != right[col-1] {
				cost = 1
			}

			row[col] = min(diagonal+cost, min(above, row[col-1])+1)
			best = min(best, row[col])
			diagonal = above
		}

		// Every path to the final cell crosses this row. If no cell in the
		// band is within the limit, subsequent rows cannot produce a match.
		if best > limit {
			return false
		}

		if end < len(right) {
			row[end+1] = limit + 1
		}
	}

	return row[len(right)] <= limit
}

// A wide band visits more cells than the bit-vector algorithm. Each block
// processes 64 columns at once; allow for its higher per-block overhead.
// Keep the bounded algorithm for Unicode outside the bit-vector table and for
// inputs exceeding the fixed carry buffers used by Distance.
func useBitVector(left []rune, right []rune, limit int) bool {
	blocks := (len(right) + 63) / 64
	if len(right) <= 64 || limit < 2*blocks || len(left) > phcMhcSize*64 {
		return false
	}

	for _, text := range [][]rune{left, right} {
		for _, char := range text {
			if char < 0 || char >= peqSize {
				return false
			}
		}
	}

	return true
}

// Removing equal prefixes and suffixes preserves Levenshtein distance.
func trimEqualEnds(left []rune, right []rune) ([]rune, []rune) {
	start := 0
	for start < min(len(left), len(right)) && left[start] == right[start] {
		start++
	}

	left, right = left[start:], right[start:]
	for len(left) != 0 && len(right) != 0 && left[len(left)-1] == right[len(right)-1] {
		left, right = left[:len(left)-1], right[:len(right)-1]
	}

	return left, right
}
