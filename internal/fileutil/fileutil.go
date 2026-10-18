// Package fileutil holds the one file helper both the history file and the settings file need.
package fileutil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const dirMode = 0o700

// WriteAtomic writes data to path through a temporary file in the same folder and a rename, so a crash or a full disk
// can never leave half a file, and creates the folder if it is missing. The file is readable by its owner only, because
// it may hold a webhook address.
func WriteAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("create folder: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".write-*.tmp")
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	_, writeErr := tmp.Write(data)
	// Without the sync a power cut after the rename can leave an empty file under the real name.
	syncErr := tmp.Sync()
	closeErr := tmp.Close()
	if err = errors.Join(writeErr, syncErr, closeErr); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write file: %w", err)
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("replace file: %w", err)
	}
	return nil
}
