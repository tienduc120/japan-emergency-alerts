package alert_test

import (
	"testing"

	"github.com/tienduc120/japan-emergency-alerts/internal/alert"
)

func TestEveryKindHasADistinctSlugAndLabel(t *testing.T) {
	t.Parallel()

	kinds := []alert.Kind{
		alert.KindEarlyWarning,
		alert.KindEarthquakeReport,
		alert.KindTsunami,
		alert.KindSpecialWarning,
		alert.KindVolcano,
	}
	slugs := map[string]alert.Kind{}
	labels := map[string]alert.Kind{}
	for _, k := range kinds {
		slug, label := k.String(), k.Label()
		if slug == "" || slug == "unknown" || label == "" || label == "Alert" {
			t.Errorf("kind %d has no proper name: slug %q, label %q", k, slug, label)
		}
		if other, ok := slugs[slug]; ok {
			t.Errorf("kinds %d and %d share the slug %q", k, other, slug)
		}
		if other, ok := labels[label]; ok {
			t.Errorf("kinds %d and %d share the label %q", k, other, label)
		}
		slugs[slug] = k
		labels[label] = k
	}
}

func TestAnInvalidKindIsNamedUnknown(t *testing.T) {
	t.Parallel()

	if got := alert.Kind(0).String(); got != "unknown" {
		t.Errorf("String() = %q, want %q", got, "unknown")
	}
	if got := alert.Kind(0).Label(); got != "Alert" {
		t.Errorf("Label() = %q, want %q", got, "Alert")
	}
}
