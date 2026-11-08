package a

import "os"

// F removes a file.
func F(name string) error {
	if err := os.Remove(name); err != nil {
		return err
	}
	return nil
}

// G removes a file that may already be gone.
func G(name string) error {
	if err := os.Remove(name); err != nil {
		// best effort: a file that cannot be removed is left for the next run
		return nil
	}
	return nil
}
