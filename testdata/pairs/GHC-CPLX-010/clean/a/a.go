package a

// Summary counts the positive values.
func Summary(values []int) int {
	n := 0
	for _, v := range values {
		if v > 0 {
			n++
		}
	}
	return n
}

// Positives counts the values above zero, less those below.
func Positives(xs []int) int {
	count := 0
	for _, x := range xs {
		if x > 0 {
			count++
		} else if x < 0 {
			count--
		}
	}
	return count
}
