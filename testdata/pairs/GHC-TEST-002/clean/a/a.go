package a

func A(x int) int {
	if x > 0 {
		return 1
	}
	return 2
}

func B(x int) int {
	y := x * 2
	y++
	return y
}
