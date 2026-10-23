package startup_test

import (
	"testing"

	"github.com/tienduc120/japan-emergency-alerts/internal/startup"
)

func TestCommandQuotesThePathAndAddsTheMarker(t *testing.T) {
	t.Parallel()

	got := startup.Command(`C:\Program Files\x\alerts.exe`)

	if want := `"C:\Program Files\x\alerts.exe" autostart`; got != want {
		t.Errorf(
			"Command = %q, want %q: a path with spaces must be quoted, and the marker tells Windows' start apart",
			got,
			want,
		)
	}
}

func TestExeOfReadsTheProgramBack(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		command string
		exe     string
		ok      bool
	}{
		"written by Command": {`"C:\Program Files\x\alerts.exe" autostart`, `C:\Program Files\x\alerts.exe`, true},
		"quoted, no marker":  {`"C:\x\alerts.exe"`, `C:\x\alerts.exe`, true},
		"unquoted":           {`C:\x\alerts.exe autostart`, `C:\x\alerts.exe`, true},
		"quote never closed": {`"C:\x\alerts.exe autostart`, "", false},
		"empty":              {"", "", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			exe, ok := startup.ExeOf(tc.command)

			if exe != tc.exe || ok != tc.ok {
				t.Errorf("ExeOf(%q) = %q, %v, want %q, %v", tc.command, exe, ok, tc.exe, tc.ok)
			}
		})
	}
}
