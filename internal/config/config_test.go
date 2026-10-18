package config_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/tienduc120/japan-emergency-alerts/internal/config"
	"github.com/tienduc120/japan-emergency-alerts/internal/intensity"
	"github.com/tienduc120/japan-emergency-alerts/internal/region"
)

const (
	token   = "very-secret-token"
	webhook = "https://discord.com/api/webhooks/123/" + token
)

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func prefecture(t *testing.T, name string) region.Location {
	t.Helper()
	p, ok := region.Lookup(name)
	if !ok {
		t.Fatalf("no prefecture named %q", name)
	}
	return region.InPrefecture(p)
}

func TestDefaultsAreTokyoAndFiveLower(t *testing.T) {
	t.Parallel()

	got, err := config.FromEnv(env(map[string]string{"WEBHOOK_URL": webhook}))
	if err != nil {
		t.Fatal(err)
	}
	if got.Location != prefecture(t, "Tokyo") {
		t.Errorf("Location = %v, want Tokyo", got.Location)
	}
	if got.MinIntensity != intensity.FiveLower {
		t.Errorf("MinIntensity = %v, want 5-", got.MinIntensity)
	}
	if got.WebhookURL.String() != webhook {
		t.Errorf("WebhookURL = %v, want the one given", got.WebhookURL)
	}
}

func TestLocationCanBeAPrefectureNameOrAllJapan(t *testing.T) {
	t.Parallel()

	tests := map[string]region.Location{
		"Osaka":  prefecture(t, "Osaka"),
		"東京都":    prefecture(t, "Tokyo"),
		"all":    region.Everywhere(),
		"ALL":    region.Everywhere(),
		" Kyoto": prefecture(t, "Kyoto"), //nolint:gocritic // the space is the point: input is trimmed
	}
	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := config.FromEnv(env(map[string]string{"WEBHOOK_URL": webhook, "LOCATION": name}))
			if err != nil {
				t.Fatal(err)
			}
			if got.Location != want {
				t.Errorf("LOCATION=%q gave %v, want %v", name, got.Location, want)
			}
		})
	}
}

func TestMinIntensityCanBeChanged(t *testing.T) {
	t.Parallel()

	tests := map[string]intensity.Scale{"6-": intensity.SixLower, "5強": intensity.FiveUpper, "3": intensity.Three}
	for in, want := range tests {
		t.Run(in, func(t *testing.T) {
			t.Parallel()

			got, err := config.FromEnv(env(map[string]string{"WEBHOOK_URL": webhook, "MIN_INTENSITY": in}))
			if err != nil {
				t.Fatal(err)
			}
			if got.MinIntensity != want {
				t.Errorf("MIN_INTENSITY=%q gave %v, want %v", in, got.MinIntensity, want)
			}
		})
	}
}

func TestEveryProblemIsReportedTogether(t *testing.T) {
	t.Parallel()

	_, err := config.FromEnv(env(map[string]string{"LOCATION": "Atlantis", "MIN_INTENSITY": "9"}))

	if err == nil {
		t.Fatal("a bad configuration must not start the program")
	}
	for _, want := range []string{"LOCATION", "MIN_INTENSITY", "WEBHOOK_URL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %s: fixing one setting at a time wastes the user's time:\n%v", want, err)
		}
	}
}

func TestAnUnknownLocationPointsToTheGuide(t *testing.T) {
	t.Parallel()

	_, err := config.FromEnv(env(map[string]string{"WEBHOOK_URL": webhook, "LOCATION": "Atlantis"}))

	if err == nil || !strings.Contains(err.Error(), "README") {
		t.Errorf("err = %v, want it to say where the list of valid names is", err)
	}
}

func TestTheWebhookMustBeAnHTTPURLAndTheErrorNeverQuotesIt(t *testing.T) {
	t.Parallel()

	for _, bad := range []string{
		"ftp://example.com/" + token,
		"discord.com/api/webhooks/" + token,
		"https:///" + token,
		"http://bad host/" + token,
	} {
		t.Run(bad, func(t *testing.T) {
			t.Parallel()

			_, err := config.FromEnv(env(map[string]string{"WEBHOOK_URL": bad}))

			if err == nil {
				t.Fatalf("%q must be refused", bad)
			}
			if strings.Contains(err.Error(), token) {
				t.Errorf("the error quotes the webhook, whose path is a secret:\n%v", err)
			}
		})
	}
}

func TestLoggingAConfigNeverShowsTheWebhookToken(t *testing.T) {
	t.Parallel()

	cfg, err := config.FromEnv(env(map[string]string{"WEBHOOK_URL": webhook}))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer

	slog.New(slog.NewJSONHandler(&out, nil)).InfoContext(t.Context(), "config", "config", cfg)

	got := out.String()
	for _, want := range []string{"Tokyo", "5-", "discord.com"} {
		if !strings.Contains(got, want) {
			t.Errorf("log lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, token) {
		t.Errorf("log shows the webhook token:\n%s", got)
	}
}

func TestLoggingAnEmptyConfigDoesNotPanic(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	slog.New(slog.NewJSONHandler(&out, nil)).InfoContext(t.Context(), "config", "config", config.Config{})

	if out.Len() == 0 {
		t.Error("nothing was logged")
	}
}
