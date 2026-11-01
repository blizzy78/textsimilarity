package textsimilarity

import "context"

// A small reachability probe is a necessary bound, not the chosen alignment.
// Bound both dimensions independently of unusually large user options. Exact
// anchors are selected only after a candidate survives this inexpensive check.
func canReachLinePrefix(ctx context.Context, source *fileToCheck, sourceStart int, peer *fileToCheck, peerStart int, opts *Options) bool {
	const maximumProbe = 16
	if opts.MaxLineDistance > maximumProbe || opts.MinSimilarLines <= 1 {
		return !contextDone(ctx)
	}

	rows := min(opts.MinSimilarLines-1, maximumProbe)
	budget := opts.MaxLineDistance
	peerLimit := rows*(budget+1) + budget

	var positions []int
	for next := peerStart + 1; len(positions) < peerLimit; {
		line := nextAcceptedLine(ctx, peer, next, opts)
		if line < 0 {
			break
		}

		positions = append(positions, line)
		next = line + 1
	}

	previous := make([]int, len(positions)+1)

	current := make([]int, len(previous))
	for column := range previous {
		previous[column] = min(column, budget+1)
	}

	probe := linePrefixProbe{peer: peer, positions: positions, previous: previous, current: current, budget: budget}

	next := sourceStart + 1
	for range rows {
		line := nextAcceptedLine(ctx, source, next, opts)
		if line < 0 {
			return false
		}

		next = line + 1
		if !probe.advance(ctx, source.f.lines[line]) {
			return false
		}
	}

	return !contextDone(ctx)
}

type linePrefixProbe struct {
	peer      *fileToCheck
	positions []int
	previous  []int
	current   []int
	budget    int
}

func (probe *linePrefixProbe) advance(ctx context.Context, source *fileLine) bool {
	probe.current[0] = min(probe.previous[0]+1, probe.budget+1)
	possible := probe.current[0] <= probe.budget

	for column, peerLine := range probe.positions {
		if contextDone(ctx) {
			return false
		}

		diagonal := probe.budget + 1
		if probe.previous[column] <= probe.budget {
			diagonal = probe.previous[column] + 1
			if linesSimilarity(source, probe.peer.f.lines[peerLine]) == EqualSimilarityLevel {
				diagonal = 0
			}
		}

		probe.current[column+1] = min(probe.budget+1, diagonal, probe.previous[column+1]+1, probe.current[column]+1)
		possible = possible || probe.current[column+1] <= probe.budget
	}

	if !possible {
		return false
	}

	probe.previous, probe.current = probe.current, probe.previous

	return true
}
