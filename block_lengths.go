package textsimilarity

func scalarLengthsMatch(left, right []int32, limit int32) bool {
	for idx, value := range left {
		difference := value - right[idx]
		if difference < -limit || difference > limit {
			return false
		}
	}

	return true
}
