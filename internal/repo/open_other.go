//go:build !unix

package repo

// openFlags: there are no FIFOs to block on outside unix.
const openFlags = 0
