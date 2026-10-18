package state_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/tienduc120/japan-emergency-alerts/internal/async"
	"github.com/tienduc120/japan-emergency-alerts/internal/state"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func noon() time.Time { return time.Date(2026, time.October, 18, 3, 0, 0, 0, time.UTC) }

func TestAFirstRunHasNoHistory(t *testing.T) {
	t.Parallel()

	s, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !s.Horizon().IsZero() || len(s.Sent()) != 0 {
		t.Errorf("horizon %v, sent %v, want nothing on a first run", s.Horizon(), s.Sent())
	}
}

func TestWhatWasSavedIsThereAfterARestart(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "nested", "dir", "state.json")
	s, err := state.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	if err = s.SetHorizon(noon()); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkSent("alert-1", noon().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	again, err := state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Horizon().Equal(noon()) {
		t.Errorf("Horizon = %v, want %v", again.Horizon(), noon())
	}
	if at, ok := again.Sent()["alert-1"]; !ok || !at.Equal(noon().Add(time.Minute)) {
		t.Errorf("Sent = %v, want alert-1 at %v", again.Sent(), noon().Add(time.Minute))
	}
}

func TestACorruptFileGivesAUsableEmptyStoreAndAnError(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := state.Open(path)

	if err == nil {
		t.Error("want an error to log: the history was lost")
	}
	if s == nil || !s.Horizon().IsZero() {
		t.Fatalf("store %v, want a usable empty store so the program can still run", s)
	}
	if err = s.MarkSent("x", noon()); err != nil {
		t.Fatalf("the next save must replace the corrupt file: %v", err)
	}
	if _, err = state.Open(path); err != nil {
		t.Errorf("the file is still unreadable after a save: %v", err)
	}
}

func TestAFileWithAByteOrderMarkIsStillRead(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	// Windows editors, and PowerShell, put these three invisible bytes at the start of a UTF-8 file.
	contents := "\xef\xbb\xbf" + `{"version":1,"horizon":"2026-10-18T03:00:00Z","sent":{}}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := state.Open(path)
	if err != nil {
		t.Fatalf("Open = %v, want the file read: a user editing it in Notepad must not lose the history", err)
	}
	if !s.Horizon().Equal(noon()) {
		t.Errorf("Horizon = %v, want %v", s.Horizon(), noon())
	}
}

func TestSentAlertsOlderThanADayAreDroppedSoTheFileStaysSmall(t *testing.T) {
	t.Parallel()
	s, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkSent("old", noon()); err != nil {
		t.Fatal(err)
	}

	if err = s.MarkSent("new", noon().Add(25*time.Hour)); err != nil {
		t.Fatal(err)
	}

	sent := s.Sent()
	if _, ok := sent["old"]; ok || len(sent) != 1 {
		t.Errorf("Sent = %v, want only the new alert", sent)
	}
}

func TestWithNoPathNothingIsWrittenAndNothingFails(t *testing.T) {
	t.Parallel()
	s, err := state.Open("")
	if err != nil {
		t.Fatal(err)
	}

	if err = s.SetHorizon(noon()); err != nil {
		t.Errorf("SetHorizon = %v, want nil: no file is not a failure", err)
	}
	if err = s.MarkSent("x", noon()); err != nil {
		t.Errorf("MarkSent = %v, want nil", err)
	}

	if !s.Horizon().Equal(noon()) || len(s.Sent()) != 1 {
		t.Errorf("the store must still remember within the run: %v, %v", s.Horizon(), s.Sent())
	}
}

func TestAPathThatCannotBeWrittenIsAnError(t *testing.T) {
	t.Parallel()
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, _ := state.Open(filepath.Join(blocker, "state.json"))

	if err := s.SetHorizon(noon()); err == nil {
		t.Error("want an error: the caller needs to know the history is not being kept")
	}
}

func TestSavingNeverLeavesAPartialFileOrLitter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	s, err := state.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	for i := range 20 {
		if err = s.MarkSent("alert-"+strconv.Itoa(i), noon().Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "state.json" {
		t.Errorf("directory holds %v, want only state.json", entries)
	}
	if _, err = state.Open(path); err != nil {
		t.Errorf("the saved file does not read back: %v", err)
	}
}

func TestSeveralSourcesCanRecordAtOnce(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	tasks := make([]func(context.Context) error, 8)
	for i := range tasks {
		tasks[i] = func(context.Context) error {
			for j := range 10 {
				if markErr := s.MarkSent(strconv.Itoa(i)+"-"+strconv.Itoa(j), noon()); markErr != nil {
					return markErr
				}
			}
			return s.SetHorizon(noon())
		}
	}

	if err = async.Run(t.Context(), tasks...); err != nil {
		t.Fatal(err)
	}

	again, err := state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Sent()) != 80 {
		t.Errorf("%d alerts remembered, want 80", len(again.Sent()))
	}
}
