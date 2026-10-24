package a

// F classifies its arguments in one function.
func F(xs []int, limit int) int {
	n := 0
	for _, x := range xs { // +1
		if x > limit { // +2
			for i := 0; i < x; i++ { // +3
				if i%2 == 0 && i%3 == 0 { // +4, +1
					n++
				} else if i%5 == 0 { // +1
					n--
				} else { // +1
					n += 2
				}
				if n > 1000 { // +4
					break
				}
			}
		} else if x < 0 { // +1
			n--
		}
	}
	return n // 18
}
