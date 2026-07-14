// Command barmux is one progress dashboard for many processes: children
// write a dumb line protocol to a pipe, the parent renders bars on a TTY
// and clean milestone logs everywhere else.
package main

import (
	"os"

	"github.com/JaydenCJ/barmux/internal/cli"
)

// isTTY reports whether stdout is a character device (a live terminal).
func isTTY() bool {
	info, err := os.Stdout.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func main() {
	app := &cli.App{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Getenv: os.Getenv,
		IsTTY:  isTTY,
	}
	os.Exit(app.Main(os.Args[1:]))
}
