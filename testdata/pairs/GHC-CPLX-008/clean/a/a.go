package a

import "errors"

// F syncs the cache.
func F() error {
	if cache == nil {
		return errors.New("no cache")
	}
	return nil
}

var cache map[string]string
