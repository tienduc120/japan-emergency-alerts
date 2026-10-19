package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func logEnv(dir string) func(string) string {
	return func(key string) string {
		if key == "CONFIG_FILE" {
			return filepath.Join(dir, "config.json")
		}
		return ""
	}
}

func TestWithNoTerminalTheLogGoesToAFileBesideTheSettings(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	out := openLogFile(logEnv(dir))
	if _, err := out.Write([]byte("started\n")); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "log.txt"))
	if err != nil || string(got) != "started\n" {
		t.Errorf("log %q (%v), want what was written, in log.txt beside the settings", got, err)
	}
}

func TestTheLogOfTheLastRunIsReplacedSoTheFileStaysSmall(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "log.txt"), []byte("an old run, long ago\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out := openLogFile(logEnv(dir))
	_, _ = out.Write([]byte("new\n"))
	_ = out.Close()

	if got, _ := os.ReadFile(filepath.Join(dir, "log.txt")); string(got) != "new\n" {
		t.Errorf("log %q, want only the new run", got)
	}
}

func TestWithNowhereToKeepALogTheOutputIsDroppedNotFatal(t *testing.T) {
	t.Parallel()

	out := openLogFile(func(string) string { return "" })

	if n, err := out.Write([]byte("nobody reads this")); err != nil || n == 0 {
		t.Errorf("wrote %d bytes, %v, want the output swallowed without a complaint", n, err)
	}
	if err := out.Close(); err != nil {
		t.Errorf("close = %v", err)
	}
}

func TestTheKeyToTheSettingsPageNeverReachesTheLog(t *testing.T) {
	t.Parallel()
	var kept strings.Builder

	_, err := redactKeys(
		&kept,
	).Write([]byte("Settings page: http://127.0.0.1:47600/?key=899da47968fb68258acdf53d01fadb97\n"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(kept.String(), "899da479") || !strings.Contains(kept.String(), "127.0.0.1:47600") {
		t.Errorf("log %q, want the address without its key: a log is something people send to others", kept.String())
	}
}
