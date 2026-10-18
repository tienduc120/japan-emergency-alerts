package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/tienduc120/japan-emergency-alerts/internal/config"
)

func TestTheHeartbeatIsOffUnlessAskedFor(t *testing.T) {
	t.Parallel()

	got, err := config.FromEnv(env(map[string]string{"WEBHOOK_URL": webhook}))
	if err != nil {
		t.Fatal(err)
	}
	if got.HeartbeatEvery != 0 {
		t.Errorf(
			"HeartbeatEvery = %v, want 0: a status message nobody asked for is noise in the alert channel",
			got.HeartbeatEvery,
		)
	}
}

func TestTheHeartbeatCanBeChangedOrSwitchedOff(t *testing.T) {
	t.Parallel()

	tests := map[string]time.Duration{
		"6": 6 * time.Hour, "0": 0, "168": 168 * time.Hour,
		" 12 ": 12 * time.Hour, //nolint:gocritic // the spaces are the point: input is trimmed
	}
	for in, want := range tests {
		t.Run(in, func(t *testing.T) {
			t.Parallel()

			got, err := config.FromEnv(env(map[string]string{"WEBHOOK_URL": webhook, "HEARTBEAT_HOURS": in}))
			if err != nil {
				t.Fatal(err)
			}
			if got.HeartbeatEvery != want {
				t.Errorf("HEARTBEAT_HOURS=%q gave %v, want %v", in, got.HeartbeatEvery, want)
			}
		})
	}
}

func TestABadHeartbeatIsRefusedByName(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"abc", "-1", "169", "1.5", "6h"} {
		t.Run(in, func(t *testing.T) {
			t.Parallel()

			_, err := config.FromEnv(env(map[string]string{"WEBHOOK_URL": webhook, "HEARTBEAT_HOURS": in}))

			if err == nil || !strings.Contains(err.Error(), "HEARTBEAT_HOURS") {
				t.Errorf("HEARTBEAT_HOURS=%q: err = %v, want a refusal naming it", in, err)
			}
		})
	}
}

func TestTheStateFileCanBeChosen(t *testing.T) {
	t.Parallel()

	got, err := config.FromEnv(
		env(map[string]string{"WEBHOOK_URL": webhook, "STATE_FILE": " /data/alerts/state.json "}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.StateFile != "/data/alerts/state.json" {
		t.Errorf("StateFile = %q, want the path given", got.StateFile)
	}
}

func TestTheStateFileDefaultsToTheUsersConfigFolder(t *testing.T) {
	t.Parallel()
	base := "/home/someone"

	got, err := config.FromEnv(env(map[string]string{
		"WEBHOOK_URL": webhook, "APPDATA": base, "XDG_CONFIG_HOME": base, "HOME": base,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.StateFile, "japan-emergency-alerts") || !strings.HasSuffix(got.StateFile, "state.json") {
		t.Errorf("StateFile = %q, want state.json in a japan-emergency-alerts folder", got.StateFile)
	}
	if !strings.Contains(got.StateFile, "someone") {
		t.Errorf("StateFile = %q, want it under the user's own folder", got.StateFile)
	}
}

func TestWithNoHomeFolderNoHistoryIsKept(t *testing.T) {
	t.Parallel()

	got, err := config.FromEnv(env(map[string]string{"WEBHOOK_URL": webhook}))
	if err != nil {
		t.Fatal(err)
	}
	if got.StateFile != "" {
		t.Errorf("StateFile = %q, want empty: with nowhere to write, the program runs without history", got.StateFile)
	}
}
