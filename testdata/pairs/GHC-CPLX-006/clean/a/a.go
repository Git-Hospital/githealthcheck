package a

// F doubles and adds one.
func F(x int) int { return helper(x * 2) }

func helper(x int) int { return x + 1 }
