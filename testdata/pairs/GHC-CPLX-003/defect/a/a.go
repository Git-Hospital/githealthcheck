package a

// F nests one if per argument.
func F(a, b, c, d, e bool) int {
	if a {
		if b {
			if c {
				if d {
					if e {
						return 1
					}
				}
			}
		}
	}
	return 0
}
