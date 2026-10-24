package a

// F classifies its arguments, one function per level.
func F(xs []int, limit int) int {
	n := 0
	for _, x := range xs { // +1
		if x > limit { // +2
			n += count(x)
		} else if x < 0 { // +1
			n--
		}
	}
	return n // 4
}

func count(x int) int {
	n := 0
	for i := 0; i < x; i++ { // +1
		if i%2 == 0 && i%3 == 0 { // +2, +1
			n++
		} else if i%5 == 0 { // +1
			n--
		} else { // +1
			n += 2
		}
		if n > 1000 { // +2
			break
		}
	}
	return n // 8
}
