package fileutil_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tienduc120/japan-emergency-alerts/internal/fileutil"
)

func TestWriteAtomicCreatesTheFileAndItsFolders(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "deep", "er", "file.json")

	if err := fileutil.WriteAtomic(path, []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil || string(got) != `{"a":1}` {
		t.Errorf("read %q, %v, want the data written", got, err)
	}
}

func TestWriteAtomicReplacesWhatWasThereAndLeavesNoLitter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "file.json")
	for _, content := range []string{"one", "two", "three"} {
		if err := fileutil.WriteAtomic(path, []byte(content)); err != nil {
			t.Fatal(err)
		}
	}

	got, _ := os.ReadFile(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "three" || len(entries) != 1 {
		t.Errorf("content %q in a folder holding %v, want only the latest content and no temporary files", got, entries)
	}
}

func TestWriteAtomicReportsAPathItCannotUse(t *testing.T) {
	t.Parallel()
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := fileutil.WriteAtomic(filepath.Join(blocker, "file.json"), []byte("x")); err == nil {
		t.Error("want an error when the folder cannot be created")
	}
}
