package clock_test

import (
	"testing"
	"time"

	"github.com/tienduc120/japan-emergency-alerts/internal/clock"
)

func TestSystemReadsTheRealTime(t *testing.T) {
	t.Parallel()

	before := time.Now()
	got := clock.System{}.Now()
	after := time.Now()

	if got.Before(before) || got.After(after) {
		t.Errorf("Now() = %v, want between %v and %v", got, before, after)
	}
}
