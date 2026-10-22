package main

import (
	"io"
	"os"
	"path/filepath"

	"github.com/tienduc120/japan-emergency-alerts/internal/config"
)

const logDirMode = 0o700

// discardCloser is a log that goes nowhere, for when there is no place to keep one.
type discardCloser struct{ io.Writer }

func (discardCloser) Close() error { return nil }

// openLogFile starts a log beside the settings for a program that has no terminal to print on. Each run replaces the
// last, so the file stays small. Having nowhere to write is no reason to stop, so it then drops the output.
func openLogFile(getenv func(string) string) io.WriteCloser {
	path := config.LogFilePath(getenv)
	if path == "" {
		return discardCloser{io.Discard}
	}
	if err := os.MkdirAll(filepath.Dir(path), logDirMode); err != nil {
		return discardCloser{io.Discard}
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return discardCloser{io.Discard}
	}
	return file
}
