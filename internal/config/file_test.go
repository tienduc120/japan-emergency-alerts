package config_test

import (
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tienduc120/japan-emergency-alerts/internal/config"
	"github.com/tienduc120/japan-emergency-alerts/internal/intensity"
)

const settingsPath = "/settings/config.json"

// files is a pretend disk holding only the settings file.
func files(contents string) func(string) ([]byte, error) {
	return func(path string) ([]byte, error) {
		if path != settingsPath {
			return nil, fs.ErrNotExist
		}
		return []byte(contents), nil
	}
}

func noFile(string) ([]byte, error) { return nil, fs.ErrNotExist }

func withFile(extra map[string]string) func(string) string {
	values := map[string]string{"CONFIG_FILE": settingsPath}
	maps.Copy(values, extra)
	return env(values)
}

func TestProblemsListsEachSettingThatIsWrongOnce(t *testing.T) {
	t.Parallel()

	_, err := config.FromEnv(
		env(map[string]string{"LOCATION": "Atlantis", "MIN_INTENSITY": "9", "HEARTBEAT_HOURS": "x"}),
	)

	var fields []string
	for _, p := range config.Problems(err) {
		fields = append(fields, p.Field)
	}
	slices.Sort(fields)
	want := []string{"HEARTBEAT_HOURS", "LOCATION", "MIN_INTENSITY", "WEBHOOK_URL"}
	if !slices.Equal(fields, want) {
		t.Errorf("fields = %v, want %v: a settings page shows each problem beside its own box", fields, want)
	}
	if err == nil || !strings.Contains(err.Error(), "WEBHOOK_URL is required") {
		t.Errorf("err = %v, want the same plain sentences as before", err)
	}
}

func TestAProblemWithNoSettingBehindItIsNotLost(t *testing.T) {
	t.Parallel()

	if got := config.Problems(errors.New("something else")); len(got) != 0 {
		t.Errorf("Problems = %v, want none for an error that is not about a setting", got)
	}
	if got := config.Problems(nil); len(got) != 0 {
		t.Errorf("Problems(nil) = %v, want none", got)
	}
}

func TestTheSettingsFileSuppliesWhatTheEnvironmentDoesNot(t *testing.T) {
	t.Parallel()
	contents := `{"webhook_url": "` + webhook + `", "location": "Osaka, 26", "min_intensity": "6-"}`

	got := config.Load(withFile(nil), files(contents))

	if got.Problems != nil || got.FileErr != nil {
		t.Fatalf("problems %v, file error %v, want a usable configuration", got.Problems, got.FileErr)
	}
	if got.Config.WebhookURL.String() != webhook || got.Config.MinIntensity != intensity.SixLower {
		t.Errorf("webhook %v, intensity %v, want the file's values", got.Config.WebhookURL, got.Config.MinIntensity)
	}
	if want := prefectures(t, "Osaka", "Kyoto"); got.Config.Location != want {
		t.Errorf("Location = %v, want %v", got.Config.Location, want)
	}
	if got.File.Location != "Osaka, 26" || got.Path != settingsPath {
		t.Errorf(
			"File.Location %q, Path %q, want the file as written and where it was read",
			got.File.Location,
			got.Path,
		)
	}
}

func TestTheEnvironmentWinsOverTheFileAndSaysSo(t *testing.T) {
	t.Parallel()
	contents := `{"webhook_url": "` + webhook + `", "location": "Osaka", "min_intensity": "6-"}`

	got := config.Load(withFile(map[string]string{"LOCATION": "Tokyo"}), files(contents))

	if want := prefecture(t, "Tokyo"); got.Config.Location != want {
		t.Errorf("Location = %v, want Tokyo from the environment", got.Config.Location)
	}
	if got.Config.MinIntensity != intensity.SixLower {
		t.Errorf("MinIntensity = %v, want the file's value where the environment is silent", got.Config.MinIntensity)
	}
	if !slices.Equal(got.Overridden, []string{"LOCATION"}) {
		t.Errorf(
			"Overridden = %v, want [LOCATION]: the page must say that saving a location will not take effect",
			got.Overridden,
		)
	}
}

func TestWithNoFileAndNoEnvironmentTheProblemIsTheMissingWebhook(t *testing.T) {
	t.Parallel()

	got := config.Load(withFile(nil), noFile)

	if got.FileErr != nil {
		t.Errorf("FileErr = %v, want none: a missing file is a first run", got.FileErr)
	}
	problems := config.Problems(got.Problems)
	if len(problems) != 1 || problems[0].Field != "WEBHOOK_URL" {
		t.Errorf("problems = %v, want only the missing webhook", problems)
	}
}

func TestADamagedFileIsReportedAndIgnored(t *testing.T) {
	t.Parallel()

	got := config.Load(withFile(map[string]string{"WEBHOOK_URL": webhook}), files("{not json"))

	if got.FileErr == nil || !strings.Contains(got.FileErr.Error(), settingsPath) {
		t.Errorf("FileErr = %v, want an error naming the file", got.FileErr)
	}
	if got.Problems != nil {
		t.Errorf("Problems = %v, want the environment alone to be enough to run", got.Problems)
	}
}

func TestAFileWithAnUnknownSettingIsRefusedSoATypoIsNotSilent(t *testing.T) {
	t.Parallel()

	got := config.Load(withFile(nil), files(`{"webhook_url": "`+webhook+`", "locaton": "Osaka"}`))

	if got.FileErr == nil || !strings.Contains(got.FileErr.Error(), "locaton") {
		t.Errorf("FileErr = %v, want it to name the unknown setting", got.FileErr)
	}
}

func TestAFileWithAByteOrderMarkIsStillRead(t *testing.T) {
	t.Parallel()

	got := config.Load(withFile(nil), files("\xef\xbb\xbf"+`{"webhook_url": "`+webhook+`"}`))

	if got.FileErr != nil || got.Problems != nil {
		t.Errorf("file error %v, problems %v, want the file read: Notepad adds this mark", got.FileErr, got.Problems)
	}
}

func TestTheFileIsKeptInTheSameFolderAsTheHistory(t *testing.T) {
	t.Parallel()
	base := "/home/someone"
	getenv := env(map[string]string{"APPDATA": base, "XDG_CONFIG_HOME": base, "HOME": base})

	path := config.FilePath(getenv)
	history := config.StateFilePath(getenv)

	if filepath.Base(path) != "config.json" || filepath.Dir(path) != filepath.Dir(history) {
		t.Errorf("path %q and history %q, want config.json beside state.json", path, history)
	}
	if config.FilePath(env(nil)) != "" {
		t.Error("with no home folder there is nowhere to keep settings, so the path must be empty")
	}
}

func TestTheSettingsAddressIsKeptBesideTheSettingsFile(t *testing.T) {
	t.Parallel()
	chosen := env(map[string]string{"CONFIG_FILE": "/x/y.json"})

	got := config.AddressFilePath(chosen)

	if filepath.Dir(got) != filepath.Dir(config.FilePath(chosen)) || filepath.Base(got) != "settings-address.txt" {
		t.Errorf("AddressFilePath = %q, want settings-address.txt in the settings file's folder", got)
	}
	if config.AddressFilePath(env(nil)) != "" {
		t.Error("with nowhere to keep settings there is nowhere to leave the address either")
	}
}

func TestTheLogIsKeptBesideTheSettingsFile(t *testing.T) {
	t.Parallel()
	chosen := env(map[string]string{"CONFIG_FILE": "/x/y.json"})

	got := config.LogFilePath(chosen)

	if filepath.Dir(got) != filepath.Dir(config.FilePath(chosen)) || filepath.Base(got) != "log.txt" {
		t.Errorf("LogFilePath = %q, want log.txt in the settings file's folder", got)
	}
	if config.LogFilePath(env(nil)) != "" {
		t.Error("with nowhere to keep settings there is nowhere to keep a log either")
	}
}

func TestTheFilePathCanBeChosen(t *testing.T) {
	t.Parallel()

	if got := config.FilePath(env(map[string]string{"CONFIG_FILE": " /x/y.json "})); got != "/x/y.json" {
		t.Errorf("FilePath = %q, want the path given", got)
	}
}

func TestFromFileChecksAFileOnItsOwnWithoutTheEnvironment(t *testing.T) {
	t.Parallel()

	cfg, err := config.FromFile(config.File{WebhookURL: webhook, Location: "Kyoto", MinIntensity: "5+"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MinIntensity != intensity.FiveUpper || cfg.Location != prefecture(t, "Kyoto") {
		t.Errorf("config = %+v, want the file's values", cfg)
	}
	if _, err = config.FromFile(config.File{Location: "Kyoto"}); len(config.Problems(err)) != 1 {
		t.Errorf("err = %v, want exactly the missing webhook", err)
	}
}

func TestCheckJudgesAFileTogetherWithTheEnvironment(t *testing.T) {
	t.Parallel()
	onlyLocation := config.File{Location: "Kyoto"}

	withHook := config.Check(env(map[string]string{"WEBHOOK_URL": webhook}), onlyLocation)
	withoutHook := config.Check(env(nil), onlyLocation)

	if withHook != nil {
		t.Errorf("Check = %v, want nil: the environment supplies the webhook the page left empty", withHook)
	}
	if got := config.Problems(withoutHook); len(got) != 1 || got[0].Field != "WEBHOOK_URL" {
		t.Errorf("Check = %v, want exactly the missing webhook", withoutHook)
	}
}

func TestParseWebhookAcceptsAnHTTPAddressAndNamesWhatIsWrongWithOthers(t *testing.T) {
	t.Parallel()

	u, err := config.ParseWebhook(" " + webhook + " ")
	if err != nil || u.String() != webhook {
		t.Errorf("ParseWebhook = %v, %v, want the address", u, err)
	}
	for _, bad := range []string{"", "discord.com/x", "ftp://x/y", "http://bad host/" + token} {
		_, err = config.ParseWebhook(bad)
		if len(config.Problems(err)) != 1 || strings.Contains(err.Error(), token) {
			t.Errorf("ParseWebhook(%q) = %v, want one problem that does not quote the address", bad, err)
		}
	}
}

func TestSaveWritesAFileLoadReadsBack(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "new folder", "config.json")
	saved := config.File{WebhookURL: webhook, Location: "Tokyo,Kanagawa", MinIntensity: "5-"}

	if err := config.Save(path, saved); err != nil {
		t.Fatal(err)
	}

	got := config.Load(env(map[string]string{"CONFIG_FILE": path}), os.ReadFile)
	if got.FileErr != nil || got.Problems != nil || got.File != saved {
		t.Errorf("file error %v, problems %v, file %+v, want what was saved", got.FileErr, got.Problems, got.File)
	}
	if want := prefectures(t, "Tokyo", "Kanagawa"); got.Config.Location != want {
		t.Errorf("Location = %v, want %v", got.Config.Location, want)
	}
}

func TestSaveWithNowhereToSaveIsAnError(t *testing.T) {
	t.Parallel()

	if err := config.Save("", config.File{WebhookURL: webhook}); err == nil {
		t.Error("want an error: the page must say settings cannot be kept, not pretend they were")
	}
}
