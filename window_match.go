package textsimilarity

func matchingWindows(values []uint64, source uint64, limit int) uint64 {
	var matches uint64

	for idx, value := range values {
		if !incompatibleLengths(value, source, uint64(limit)) { //nolint:gosec // supported distances are 1..32
			matches |= uint64(1) << uint(idx)
		}
	}

	return matches
}

func incompatibleLengths(candidate uint64, source uint64, limit uint64) bool {
	const (
		low  = uint64(0x7f7f7f7f7f7f7f7f)
		high = ^low
		one  = uint64(0x0101010101010101)
	)

	difference := ((candidate&low | high) - (source & low)) ^ ((candidate ^ source ^ high) & high)
	shifted := ((difference & low) + limit*one) ^ (difference & high)

	return (shifted|((shifted&low)+(127-2*limit)*one))&high != 0
}
