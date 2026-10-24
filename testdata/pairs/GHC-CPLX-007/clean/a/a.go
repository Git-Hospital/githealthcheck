package a

import "fmt"

// F prints.
func F(x int) {
	// Print without a newline: the caller adds one.
	fmt.Print(x)
}
