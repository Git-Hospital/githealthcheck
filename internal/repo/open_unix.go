//go:build unix

package repo

import "syscall"

// openFlags keep open from blocking on a FIFO.
const openFlags = syscall.O_NONBLOCK
