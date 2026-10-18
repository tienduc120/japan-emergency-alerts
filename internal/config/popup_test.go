package config_test

import (
	"strings"
	"testing"

	"github.com/tienduc120/japan-emergency-alerts/internal/config"
)

func TestPopupsAreOffUnlessAskedFor(t *testing.T) {
	t.Parallel()

	got, err := config.FromEnv(env(map[string]string{"WEBHOOK_URL": webhook}))

	if err != nil || got.Popup {
		t.Errorf("popup %v, err %v, want off: a window on someone's screen must be a choice", got.Popup, err)
	}
}

func TestPopupCanBeTurnedOnOrOffInTheUsualWays(t *testing.T) {
	t.Parallel()

	tests := map[string]bool{
		"on": true, "ON": true, "true": true, "yes": true, "1": true,
		" on ": true, //nolint:gocritic // the spaces are the point: input is trimmed
		"off":  false, "false": false, "no": false, "0": false, "": false,
	}
	for in, want := range tests {
		t.Run("["+in+"]", func(t *testing.T) {
			t.Parallel()

			got, err := config.FromEnv(env(map[string]string{"WEBHOOK_URL": webhook, "POPUP": in}))

			if err != nil || got.Popup != want {
				t.Errorf("POPUP=%q gave %v, %v, want %v", in, got.Popup, err, want)
			}
		})
	}
}

func TestAnUnclearPopupSettingIsRefusedByName(t *testing.T) {
	t.Parallel()

	_, err := config.FromEnv(env(map[string]string{"WEBHOOK_URL": webhook, "POPUP": "maybe"}))

	if problems := config.Problems(err); len(problems) != 1 || problems[0].Field != "POPUP" {
		t.Errorf("err = %v, want one problem naming POPUP", err)
	}
}

func TestWithPopupsOnTheWebhookIsOptional(t *testing.T) {
	t.Parallel()

	got, err := config.FromEnv(env(map[string]string{"POPUP": "on"}))
	if err != nil {
		t.Fatalf("err = %v, want a pop-up alone to be enough", err)
	}
	if got.WebhookURL != nil || !got.Popup {
		t.Errorf("webhook %v, popup %v, want no webhook and pop-ups on", got.WebhookURL, got.Popup)
	}
}

func TestAWebhookThatIsGivenIsStillCheckedWhenPopupsAreOn(t *testing.T) {
	t.Parallel()

	_, err := config.FromEnv(env(map[string]string{"POPUP": "on", "WEBHOOK_URL": "nonsense"}))

	if problems := config.Problems(err); len(problems) != 1 || problems[0].Field != "WEBHOOK_URL" {
		t.Errorf("err = %v, want the bad webhook reported", err)
	}
}

func TestWithNeitherAWebhookNorPopupsTheProgramHasNowhereToTellAnyone(t *testing.T) {
	t.Parallel()

	_, err := config.FromEnv(env(nil))

	if err == nil || !strings.Contains(err.Error(), "WEBHOOK_URL is required") ||
		!strings.Contains(err.Error(), "POPUP") {
		t.Errorf("err = %v, want it to say a webhook is needed unless POPUP is on", err)
	}
}

func TestThePopupChoiceTravelsThroughTheSettingsFile(t *testing.T) {
	t.Parallel()

	fromFile := config.Load(withFile(nil), files(`{"location": "Osaka", "popup": true}`))
	overridden := config.Load(
		withFile(map[string]string{"POPUP": "off"}),
		files(`{"location": "Osaka", "popup": true}`),
	)

	if fromFile.Problems != nil || !fromFile.Config.Popup || !fromFile.File.Popup {
		t.Errorf("problems %v, popup %v, want pop-ups on from the file alone", fromFile.Problems, fromFile.Config.Popup)
	}
	if overridden.Config.Popup || len(overridden.Overridden) != 1 || overridden.Overridden[0] != "POPUP" {
		t.Errorf(
			"popup %v, overridden %v, want the environment's off to win and be reported",
			overridden.Config.Popup,
			overridden.Overridden,
		)
	}
}
