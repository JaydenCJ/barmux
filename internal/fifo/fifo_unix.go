//go:build unix

// Package fifo wraps the small amount of platform code barmux needs:
// creating a named pipe, opening it for reading without ever seeing EOF,
// and opening it for writing without ever blocking a child process.
package fifo

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

// Create makes a named pipe at path with mode 0600 (the pipe is a local
// IPC endpoint; other users have no business writing progress into it).
func Create(path string) error {
	return syscall.Mkfifo(path, 0o600)
}

// OpenRead opens the pipe for the parent's read loop. It opens O_RDWR on
// purpose: holding a write end ourselves means the read side never hits
// EOF when the last child closes its write end, so short-lived writers
// (every `echo line > pipe` is one open/write/close) can come and go
// freely for the whole run.
func OpenRead(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDWR, 0)
}

// ErrNoReader is returned by OpenWrite when nothing is listening on the
// pipe. Emitters treat it as "no parent dashboard: silently do nothing",
// so instrumented scripts run unchanged outside barmux.
var ErrNoReader = errors.New("no reader on pipe")

// OpenWrite opens path for a single line write. FIFOs are opened
// O_NONBLOCK so a dead or absent parent can never hang the build:
// ENXIO (no reader) maps to ErrNoReader. Regular files are opened in
// append mode so `--pipe trace.log` records a replayable event stream.
func OpenWrite(path string) (*os.File, error) {
	info, err := os.Stat(path)
	if err == nil && info.Mode()&fs.ModeNamedPipe != 0 {
		f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			if errors.Is(err, syscall.ENXIO) {
				return nil, ErrNoReader
			}
			return nil, err
		}
		return f, nil
	}
	return os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
}
