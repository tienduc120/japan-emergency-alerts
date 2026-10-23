// Package startup is the start-with-Windows entry: a per-user value that starts the program when the person signs in,
// with a marker word, so the program can tell that start from a person's double-click.
package startup

import "strings"

// Marker is the word the start-with-Windows entry adds to the program's command line. A double-click never has it.
const Marker = "autostart"

// Command is the entry's value for exe: the quoted path, so one with spaces still works, and the marker.
func Command(exe string) string {
	return `"` + exe + `" ` + Marker
}

// ExeOf reads the program's path back from an entry's value, quoted or not; ok is false when there is none.
func ExeOf(command string) (string, bool) {
	command = strings.TrimSpace(command)
	if rest, quoted := strings.CutPrefix(command, `"`); quoted {
		exe, _, closed := strings.Cut(rest, `"`)
		if !closed || exe == "" {
			return "", false
		}
		return exe, true
	}
	exe := strings.TrimSpace(strings.TrimSuffix(command, " "+Marker))
	return exe, exe != ""
}
