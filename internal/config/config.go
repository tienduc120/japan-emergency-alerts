// Package config reads the program's settings and refuses a bad combination up front. Settings come from environment
// variables, from a settings file the settings page writes, or from both, with the environment winning.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tienduc120/japan-emergency-alerts/internal/intensity"
	"github.com/tienduc120/japan-emergency-alerts/internal/region"
)

const (
	EnvLocation     = "LOCATION"
	EnvMinIntensity = "MIN_INTENSITY"
	EnvWebhookURL   = "WEBHOOK_URL"
	// EnvHeartbeatHours is how often a status message is posted, in whole hours; 0 turns it off.
	EnvHeartbeatHours = "HEARTBEAT_HOURS"
	// EnvPopup turns on a window on this computer for each alert, beside or instead of the webhook.
	EnvPopup = "POPUP"
	// EnvStateFile is where the program keeps what it must remember across a restart.
	EnvStateFile = "STATE_FILE"
	// EnvConfigFile is where the settings page saves, and the program reads, the settings file.
	EnvConfigFile = "CONFIG_FILE"

	defaultLocation     = "Tokyo"
	defaultMinIntensity = "5-"
	everywhere          = "all"

	// defaultHeartbeatHours is 0, off: a status message nobody asked for is noise in the channel that carries alerts.
	defaultHeartbeatHours = 0
	maxHeartbeatHours     = 168
	appFolder             = "japan-emergency-alerts"
	stateFileName         = "state.json"
	configFileName        = "config.json"

	locationHelp = "use English or Japanese names or numbers 1 to 47, separated by commas (Tokyo,14,大阪府), " +
		"or \"all\"; see \"Choosing your location\" in the README for the list"
)

// FieldError is one setting that is wrong. The settings page shows it beside the box it belongs to, and the console
// prints it as a plain sentence.
type FieldError struct {
	// Field is the setting's name, which is its environment variable.
	Field string
	// Problem is what is wrong with it, written to follow the name: "is required", "must be ...".
	Problem string
}

func (e FieldError) Error() string { return e.Field + " " + e.Problem }

// Problems lists the settings an error from FromEnv or Load is about, one entry per problem.
func Problems(err error) []FieldError {
	var out []FieldError
	var walk func(error)
	walk = func(e error) {
		if e == nil {
			return
		}
		if joined, ok := e.(interface{ Unwrap() []error }); ok {
			for _, inner := range joined.Unwrap() {
				walk(inner)
			}
			return
		}
		if fe, ok := errors.AsType[FieldError](e); ok {
			out = append(out, fe)
		}
	}
	walk(err)
	return out
}

// Config is what the user asked for.
type Config struct {
	Location region.Location
	// MinIntensity applies to earthquake reports only; JMA decides what the other alerts are worth sending.
	MinIntensity intensity.Scale
	// WebhookURL is nil when there is none, which is allowed only when Popup is on.
	WebhookURL *url.URL
	// Popup shows each alert in a window on this computer.
	Popup bool
	// HeartbeatEvery is how often a status message is posted; zero means never.
	HeartbeatEvery time.Duration
	// StateFile is where history is kept; empty means it is not kept.
	StateFile string
}

// FromEnv reads the settings through getenv, so a test passes a map and the program passes os.Getenv. Every problem is
// reported at once.
func FromEnv(getenv func(string) string) (Config, error) {
	location, locationErr := locationFrom(getenv)
	minIntensity, intensityErr := intensityFrom(getenv)
	popup, popupErr := popupFrom(getenv)
	webhook, webhookErr := webhookFrom(getenv, popup)
	heartbeat, heartbeatErr := heartbeatFrom(getenv)

	if err := errors.Join(locationErr, intensityErr, webhookErr, popupErr, heartbeatErr); err != nil {
		return Config{}, err
	}
	return Config{
		Location:       location,
		MinIntensity:   minIntensity,
		WebhookURL:     webhook,
		Popup:          popup,
		HeartbeatEvery: heartbeat,
		StateFile:      StateFilePath(getenv),
	}, nil
}

// LogValue keeps the webhook's path out of logs: it holds the token that lets anyone post to the channel.
func (c Config) LogValue() slog.Value {
	host := ""
	if c.WebhookURL != nil {
		host = c.WebhookURL.Hostname()
	}
	return slog.GroupValue(
		slog.String("location", c.Location.String()),
		slog.String("min_intensity", c.MinIntensity.String()),
		slog.String("webhook_host", host),
		slog.Bool("popup", c.Popup),
		slog.String("heartbeat", c.HeartbeatEvery.String()),
		slog.String("state_file", c.StateFile),
	)
}

func locationFrom(getenv func(string) string) (region.Location, error) {
	raw := strings.TrimSpace(getenv(EnvLocation))
	if raw == "" {
		raw = defaultLocation
	}

	var places []string
	for part := range strings.SplitSeq(raw, ",") {
		if part = strings.TrimSpace(part); part != "" {
			places = append(places, part)
		}
	}
	if len(places) == 0 {
		return region.Location{}, FieldError{EnvLocation, "has no place in it; " + locationHelp}
	}

	if slices.ContainsFunc(places, func(place string) bool { return strings.EqualFold(place, everywhere) }) {
		if len(places) > 1 {
			return region.Location{}, FieldError{
				EnvLocation, fmt.Sprintf("%q already means the whole country, so use it alone", everywhere),
			}
		}
		return region.Everywhere(), nil
	}

	var (
		found []region.Prefecture
		errs  []error
	)
	for _, place := range places {
		prefecture, ok := region.Lookup(place)
		if !ok {
			errs = append(errs, FieldError{EnvLocation, fmt.Sprintf("%q is not a prefecture; %s", place, locationHelp)})
			continue
		}
		found = append(found, prefecture)
	}
	if err := errors.Join(errs...); err != nil {
		return region.Location{}, err
	}
	return region.InPrefectures(found...), nil
}

func intensityFrom(getenv func(string) string) (intensity.Scale, error) {
	raw := strings.TrimSpace(getenv(EnvMinIntensity))
	if raw == "" {
		raw = defaultMinIntensity
	}
	scale, err := intensity.Parse(raw)
	if err != nil {
		return intensity.Zero, FieldError{
			EnvMinIntensity, fmt.Sprintf("%q is not a JMA seismic intensity; use 0 to 4, 5-, 5+, 6-, 6+ or 7", raw),
		}
	}
	return scale, nil
}

func webhookFrom(getenv func(string) string, popup bool) (*url.URL, error) {
	raw := strings.TrimSpace(getenv(EnvWebhookURL))
	if raw == "" {
		if popup {
			return nil, nil //nolint:nilnil // no webhook is a valid setting when the pop-up is on
		}
		return nil, FieldError{EnvWebhookURL, "is required, unless POPUP is on to show alerts in a window instead"}
	}
	// The parse error is dropped on purpose: it quotes the URL, and the URL's path is a secret.
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, FieldError{EnvWebhookURL, "must be an http or https URL"}
	}
	return u, nil
}

func popupFrom(getenv func(string) string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(getenv(EnvPopup))) {
	case "on", "true", "yes", "1":
		return true, nil
	case "", "off", "false", "no", "0":
		return false, nil
	default:
		return false, FieldError{EnvPopup, "must be on or off"}
	}
}

func heartbeatFrom(getenv func(string) string) (time.Duration, error) {
	raw := strings.TrimSpace(getenv(EnvHeartbeatHours))
	if raw == "" {
		return defaultHeartbeatHours * time.Hour, nil
	}
	hours, err := strconv.Atoi(raw)
	if err != nil || hours < 0 || hours > maxHeartbeatHours {
		return 0, FieldError{
			EnvHeartbeatHours, fmt.Sprintf("must be a whole number of hours from 0 (off) to %d", maxHeartbeatHours),
		}
	}
	return time.Duration(hours) * time.Hour, nil
}

// StateFilePath is where the history is kept: the STATE_FILE setting, or state.json in the user's configuration
// folder. It is empty when there is nowhere to put it, and the program then keeps no history.
func StateFilePath(getenv func(string) string) string {
	if path := strings.TrimSpace(getenv(EnvStateFile)); path != "" {
		return path
	}
	return inAppFolder(getenv, stateFileName)
}

// FilePath is where the settings file lives: the CONFIG_FILE setting, or config.json beside the history.
func FilePath(getenv func(string) string) string {
	if path := strings.TrimSpace(getenv(EnvConfigFile)); path != "" {
		return path
	}
	return inAppFolder(getenv, configFileName)
}

// inAppFolder is name inside the program's folder in the user's configuration folder, found the way os.UserConfigDir
// does but through getenv, so a test controls it. With no home folder it is empty.
func inAppFolder(getenv func(string) string, name string) string {
	var base string
	switch runtime.GOOS {
	case "windows":
		base = getenv("APPDATA")
	case "darwin":
		if home := getenv("HOME"); home != "" {
			base = filepath.Join(home, "Library", "Application Support")
		}
	default:
		base = getenv("XDG_CONFIG_HOME")
		if home := getenv("HOME"); base == "" && home != "" {
			base = filepath.Join(home, ".config")
		}
	}
	if base == "" {
		return ""
	}
	return filepath.Join(base, appFolder, name)
}
