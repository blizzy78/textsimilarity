package textsimilarity

import "testing"

func TestSimilaritySetKeepsOriginalEquality(t *testing.T) {
	// File identity matters even when names match. Levels and occurrence order
	// do not affect the existing equalSimilarities contract.
	first, second := &File{Name: "same"}, &File{Name: "same"}
	set := newSimilaritySet([]*File{first, second})
	original := &Similarity{Level: SimilarSimilarityLevel, Occurrences: []*FileOccurrence{{File: first, Start: 1, End: 4}, {File: second, Start: 5, End: 8}}}
	duplicate := &Similarity{Level: EqualSimilarityLevel, Occurrences: []*FileOccurrence{original.Occurrences[1], original.Occurrences[0]}}

	distinct := &Similarity{Occurrences: []*FileOccurrence{{File: second, Start: 1, End: 4}, {File: first, Start: 5, End: 8}}}
	if !set.add(original) || set.add(duplicate) || !set.add(distinct) {
		t.Fatal("deduplication changed file identity, ordering, or level semantics")
	}
	// Put an unequal finding into every bucket to force the collision path.
	for key := range set.buckets {
		set.buckets[key] = []*Similarity{distinct}
	}

	if !set.add(original) {
		t.Fatal("a fingerprint collision suppressed an unequal finding")
	}
}
