//go:build !unix

// Non-unix stub: barmux 0.1.0 targets platforms with POSIX named pipes
// (Linux, macOS, the BSDs). Windows named-pipe support is on the roadmap.
package fifo

import (
	"errors"
	"os"
)

// ErrNoReader mirrors the unix build's sentinel.
var ErrNoReader = errors.New("no reader on pipe")

var errUnsupported = errors.New("named pipes are not supported on this platform")

// Create is unsupported on this platform.
func Create(path string) error { return errUnsupported }

// OpenRead is unsupported on this platform.
func OpenRead(path string) (*os.File, error) { return nil, errUnsupported }

// OpenWrite falls back to regular-file append so `--pipe trace.log`
// still works for offline replay even where FIFOs do not exist.
func OpenWrite(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
}
