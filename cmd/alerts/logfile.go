package main

import (
	"io"
	"os"
	"path/filepath"
	"regexp"

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

var keyInAddress = regexp.MustCompile(`key=[0-9a-f]+`)

// redactKeys passes everything on except the key in the settings page's address: a log is something people send to
// others, and the key opens the page.
func redactKeys(w io.Writer) io.Writer { return keyRedactor{w} }

type keyRedactor struct{ w io.Writer }

func (r keyRedactor) Write(p []byte) (int, error) {
	if _, err := r.w.Write(keyInAddress.ReplaceAll(p, []byte("key=…"))); err != nil {
		return 0, err
	}
	return len(p), nil
}
