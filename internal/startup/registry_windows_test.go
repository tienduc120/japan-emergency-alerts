//go:build windows

package startup_test

import (
	"crypto/rand"
	"encoding/hex"
	"testing"

	"github.com/tienduc120/japan-emergency-alerts/internal/startup"
)

// scratch is an entry under a throwaway key, so the tests never touch the real start-up list.
func scratch(t *testing.T) *startup.Registry {
	t.Helper()
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	root := `Software\japan-emergency-alerts-test-` + hex.EncodeToString(b)
	t.Cleanup(func() {
		if err := startup.DeleteTreeForTest(root); err != nil {
			t.Errorf("removing the scratch key %s: %v", root, err)
		}
	})
	return &startup.Registry{RunKey: root + `\Run`, ApprovedKey: root + `\Approved`, Name: "Japan Emergency Alerts"}
}

func TestANewEntryIsOff(t *testing.T) {
	t.Parallel()
	r := scratch(t)

	on, exe, err := r.State()

	if on || exe != "" || err != nil {
		t.Errorf("State = %v, %q, %v, want off with no program and no error", on, exe, err)
	}
}

func TestEnableThenStateSaysOnWithThePath(t *testing.T) {
	t.Parallel()
	r := scratch(t)

	err := r.Enable(`C:\Program Files\x\alerts.exe`)
	on, exe, stateErr := r.State()

	if err != nil || stateErr != nil || !on || exe != `C:\Program Files\x\alerts.exe` {
		t.Errorf("Enable = %v; State = %v, %q, %v; want on with the program's path", err, on, exe, stateErr)
	}
}

func TestTaskManagersOffMarkShowsAsOffAndEnableClearsIt(t *testing.T) {
	t.Parallel()
	r := scratch(t)
	if err := r.Enable(`C:\x\alerts.exe`); err != nil {
		t.Fatal(err)
	}
	if err := startup.SetApprovedForTest(r, []byte{3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}

	turnedOff, _, offErr := r.State()
	enableErr := r.Enable(`C:\x\alerts.exe`)
	backOn, _, onErr := r.State()

	if turnedOff || offErr != nil {
		t.Errorf(
			"State after Task Manager turned it off = %v, %v, want off: the box must show what Windows will do",
			turnedOff,
			offErr,
		)
	}
	if !backOn || enableErr != nil || onErr != nil {
		t.Errorf(
			"State after Enable = %v (%v, %v), want on: ticking the box again must make it start again",
			backOn,
			enableErr,
			onErr,
		)
	}
}

func TestDisableRemovesTheValueAndIsFineWhenItIsMissing(t *testing.T) {
	t.Parallel()
	r := scratch(t)
	if err := r.Enable(`C:\x\alerts.exe`); err != nil {
		t.Fatal(err)
	}

	first := r.Disable()
	second := r.Disable()
	on, _, err := r.State()

	if first != nil || second != nil || on || err != nil {
		t.Errorf("Disable = %v then %v; State = %v, %v; want it gone and no error either time", first, second, on, err)
	}
}
