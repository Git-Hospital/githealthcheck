package a

import "os"

// F removes a file.
func F(name string) error {
	if err := os.Remove(name); err != nil {
	}
	return nil
}
