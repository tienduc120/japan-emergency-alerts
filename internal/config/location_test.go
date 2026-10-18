package config_test

import (
	"strings"
	"testing"

	"github.com/tienduc120/japan-emergency-alerts/internal/config"
	"github.com/tienduc120/japan-emergency-alerts/internal/region"
)

func prefectures(t *testing.T, names ...string) region.Location {
	t.Helper()
	var list []region.Prefecture
	for _, name := range names {
		p, ok := region.Lookup(name)
		if !ok {
			t.Fatalf("no prefecture named %q", name)
		}
		list = append(list, p)
	}
	return region.InPrefectures(list...)
}

func TestLocationCanBeAPrefectureNumber(t *testing.T) {
	t.Parallel()

	tests := map[string]region.Location{
		"13": prefecture(t, "Tokyo"),
		"01": prefecture(t, "Hokkaido"),
		"27": prefecture(t, "Osaka"),
	}
	for in, want := range tests {
		t.Run(in, func(t *testing.T) {
			t.Parallel()

			got, err := config.FromEnv(env(map[string]string{"WEBHOOK_URL": webhook, "LOCATION": in}))
			if err != nil {
				t.Fatal(err)
			}
			if got.Location != want {
				t.Errorf("LOCATION=%q gave %v, want %v", in, got.Location, want)
			}
		})
	}
}

func TestLocationCanBeAListOfNamesAndNumbers(t *testing.T) {
	t.Parallel()

	tests := map[string]region.Location{
		"Tokyo,Kanagawa":       prefectures(t, "Tokyo", "Kanagawa"),
		"13, 14":               prefectures(t, "Tokyo", "Kanagawa"),
		"Tokyo, 14":            prefectures(t, "Tokyo", "Kanagawa"),
		"東京都,神奈川県":             prefectures(t, "Tokyo", "Kanagawa"),
		"Kanagawa,Tokyo":       prefectures(t, "Tokyo", "Kanagawa"),
		"Tokyo,Tokyo":          prefecture(t, "Tokyo"),
		"Tokyo,":               prefecture(t, "Tokyo"),
		"Tokyo,Kyoto,Osaka":    prefectures(t, "Tokyo", "Kyoto", "Osaka"),
		" Osaka , 26 , Nara ,": prefectures(t, "Osaka", "Kyoto", "Nara"),
	}
	for in, want := range tests {
		t.Run(in, func(t *testing.T) {
			t.Parallel()

			got, err := config.FromEnv(env(map[string]string{"WEBHOOK_URL": webhook, "LOCATION": in}))
			if err != nil {
				t.Fatal(err)
			}
			if got.Location != want {
				t.Errorf("LOCATION=%q gave %v, want %v", in, got.Location, want)
			}
		})
	}
}

func TestAllJapanCannotBeMixedWithPrefectures(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"all,Tokyo", "Tokyo,all", "ALL, 13"} {
		t.Run(in, func(t *testing.T) {
			t.Parallel()

			_, err := config.FromEnv(env(map[string]string{"WEBHOOK_URL": webhook, "LOCATION": in}))

			if err == nil || !strings.Contains(err.Error(), "all") {
				t.Errorf("err = %v, want a refusal explaining that all stands alone", err)
			}
		})
	}
}

func TestEveryUnknownPlaceInAListIsReported(t *testing.T) {
	t.Parallel()

	_, err := config.FromEnv(env(map[string]string{"WEBHOOK_URL": webhook, "LOCATION": "Tokyo,Atlantis,99,Narnia"}))

	if err == nil {
		t.Fatal("one bad entry must stop the program: a silently dropped place is a place left unwatched")
	}
	for _, want := range []string{"Atlantis", "99", "Narnia"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
}

func TestAListWithNothingInItIsRefused(t *testing.T) {
	t.Parallel()

	for _, in := range []string{",", " , ,, "} {
		t.Run(in, func(t *testing.T) {
			t.Parallel()

			_, err := config.FromEnv(env(map[string]string{"WEBHOOK_URL": webhook, "LOCATION": in}))

			if err == nil || !strings.Contains(err.Error(), "LOCATION") {
				t.Errorf("LOCATION=%q: err = %v, want a refusal naming LOCATION", in, err)
			}
		})
	}
}

func TestAListedLocationReadsInCodeOrder(t *testing.T) {
	t.Parallel()

	cfg, err := config.FromEnv(env(map[string]string{"WEBHOOK_URL": webhook, "LOCATION": "Kanagawa,Tokyo"}))
	if err != nil {
		t.Fatal(err)
	}

	if got := cfg.Location.String(); got != "Tokyo, Kanagawa" {
		t.Errorf("Location = %q, want %q", got, "Tokyo, Kanagawa")
	}
}
