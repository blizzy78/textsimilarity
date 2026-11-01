package textsimilarity

import "math/bits"

type similarityFingerprint struct {
	count int
	sum   uint64
}
type similaritySet struct {
	fileIDs map[*File]uint64
	buckets map[similarityFingerprint][]*Similarity
}

func newSimilaritySet(files []*File) *similaritySet {
	set := &similaritySet{fileIDs: make(map[*File]uint64, len(files)), buckets: map[similarityFingerprint][]*Similarity{}}
	for idx, file := range files {
		set.fileIDs[file] = uint64(idx + 1)
	}

	return set
}

func (set *similaritySet) add(sim *Similarity) bool {
	key := similarityFingerprint{count: len(sim.Occurrences)}
	for _, occ := range sim.Occurrences {
		value := set.fileIDs[occ.File]*0x9e3779b97f4a7c15 ^ uint64(occ.Start) ^ bits.RotateLeft64(uint64(occ.End), 29) //nolint:gosec // internal occurrence offsets are nonnegative
		value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
		value = (value ^ (value >> 27)) * 0x94d049bb133111eb
		key.sum += value ^ (value >> 31)
	}

	for _, existing := range set.buckets[key] {
		if equalSimilarities(existing, sim) {
			return false
		}
	}

	set.buckets[key] = append(set.buckets[key], sim)

	return true
}
