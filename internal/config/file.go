package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tienduc120/japan-emergency-alerts/internal/fileutil"
)

const (
	addressFileName = "settings-address.txt"
	logFileName     = "log.txt"
)

// File is what the settings page saves. Each field is the same setting as an environment variable, written the same
// way, so one set of checks serves both.
type File struct {
	WebhookURL   string `json:"webhook_url"`
	Location     string `json:"location"`
	MinIntensity string `json:"min_intensity"`
	Popup        bool   `json:"popup"`
}

func (f File) env() map[string]string {
	values := map[string]string{
		EnvWebhookURL:   f.WebhookURL,
		EnvLocation:     f.Location,
		EnvMinIntensity: f.MinIntensity,
	}
	// An unset popup stays empty, not "off", so it never counts as overriding an environment setting.
	if f.Popup {
		values[EnvPopup] = "on"
	}
	return values
}

// ParseFile reads a settings file. An unknown setting is an error, so a typo is shown instead of silently ignored, and
// the byte-order mark that Windows editors add is skipped.
func ParseFile(data []byte) (File, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if len(bytes.TrimSpace(data)) == 0 {
		return File{}, nil
	}
	var f File
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&f); err != nil {
		return File{}, err
	}
	return f, nil
}

// FromFile checks a File on its own, without the environment, as the settings page must before it saves.
func FromFile(f File) (Config, error) {
	values := f.env()
	return FromEnv(func(key string) string { return values[key] })
}

// Check judges a settings file as it would be used: with the environment over it. The settings page uses it before it
// saves, so someone who supplies the webhook through the environment is not asked for it again.
func Check(getenv func(string) string, f File) error {
	fromFile := f.env()
	_, err := FromEnv(func(key string) string {
		if value := getenv(key); strings.TrimSpace(value) != "" {
			return value
		}
		return fromFile[key]
	})
	return err
}

// AddressFilePath is where a running copy leaves the address of its settings page, with its key, so that a second copy
// can open it for the person. It sits beside the settings file, in the same owner-only folder.
func AddressFilePath(getenv func(string) string) string {
	return besideSettings(getenv, addressFileName)
}

// LogFilePath is where a program with no terminal of its own, which is how it runs on Windows, writes what it would
// have printed. It sits beside the settings file.
func LogFilePath(getenv func(string) string) string {
	return besideSettings(getenv, logFileName)
}

func besideSettings(getenv func(string) string, name string) string {
	path := FilePath(getenv)
	if path == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(path), name)
}

// ParseWebhook checks one webhook address and returns it parsed, with the same single problem the settings give.
func ParseWebhook(raw string) (*url.URL, error) {
	return webhookFrom(func(string) string { return raw }, false)
}

// Save writes the settings file, creating its folder. With nowhere to write it says so rather than pretend.
func Save(path string, f File) error {
	if path == "" {
		return errors.New("settings cannot be kept: no user configuration folder was found; set CONFIG_FILE to a path")
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	if err = fileutil.WriteAtomic(path, append(data, '\n')); err != nil {
		return fmt.Errorf("save settings file: %w", err)
	}
	return nil
}

// Loaded is everything known about the settings at one moment.
type Loaded struct {
	// Config is usable only when Problems is nil.
	Config Config
	// Problems says why Config cannot be used: each missing or wrong setting, as Problems(err) lists them.
	Problems error
	// File is what the settings file holds, or the zero File if there is none or it could not be read.
	File File
	// FileErr is set when the settings file exists but could not be read. It is ignored, and saving replaces it.
	FileErr error
	// Path is where the settings file is, or empty when there is nowhere to keep one.
	Path string
	// Overridden names the settings the file sets but an environment variable overrides, which the page must tell the
	// user, because saving them there will change nothing.
	Overridden []string
}

// Load reads the settings: the environment over the settings file over the defaults. readFile is os.ReadFile in the
// program and a fake in tests. A file that cannot be read is reported in FileErr and the rest still loads.
func Load(getenv func(string) string, readFile func(path string) ([]byte, error)) Loaded {
	loaded := Loaded{Path: FilePath(getenv)}
	if loaded.Path != "" {
		data, err := readFile(loaded.Path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			loaded.FileErr = fmt.Errorf("settings file %s could not be read: %w", loaded.Path, err)
		default:
			file, parseErr := ParseFile(data)
			if parseErr != nil {
				loaded.FileErr = fmt.Errorf("settings file %s is not usable and will be replaced when you save: %w",
					loaded.Path, parseErr)
			} else {
				loaded.File = file
			}
		}
	}

	fromFile := loaded.File.env()
	for key, value := range fromFile {
		if value != "" && strings.TrimSpace(getenv(key)) != "" {
			loaded.Overridden = append(loaded.Overridden, key)
		}
	}
	// Map order is random; the page and tests want a steady one.
	slices.Sort(loaded.Overridden)

	layered := func(key string) string {
		if value := getenv(key); strings.TrimSpace(value) != "" {
			return value
		}
		return fromFile[key]
	}
	loaded.Config, loaded.Problems = FromEnv(layered)
	return loaded
}
