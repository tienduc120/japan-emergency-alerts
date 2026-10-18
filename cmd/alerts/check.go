package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/tienduc120/japan-emergency-alerts/internal/alert"
	"github.com/tienduc120/japan-emergency-alerts/internal/clock"
	"github.com/tienduc120/japan-emergency-alerts/internal/config"
	"github.com/tienduc120/japan-emergency-alerts/internal/filter"
)

const jstOffset = 9 * 60 * 60

// check reads what JMA published over the last few days and says what the program would have done with each
// alert for this configuration. It posts nothing, so it is safe to run any time, and it is how a person can see the
// real feeds being understood without waiting for a disaster.
func check(ctx context.Context, cfg config.Config, stdout, stderr io.Writer, sources sourceFactory) int {
	flt := filter.New(cfg.Location, cfg.MinIntensity, clock.System{})
	total, wouldSend := 0, 0
	handle := func(_ context.Context, a alert.Alert) error {
		total++
		decision := flt.Decide(a)
		verdict := "not sent: " + decision.String()
		if decision == filter.Send {
			verdict = "WOULD SEND"
			wouldSend++
		}
		fmt.Fprintf(stdout, "%s | %s | %s | %s\n", when(a), a.Kind.Label(), a.ID, verdict)
		return nil
	}

	// Problems reading a telegram are warnings worth seeing here, unlike in normal running where they go to the log.
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	for _, source := range sources(sourceConfig{Handle: handle, Logger: logger}) {
		replayer, ok := source.(replayer)
		if !ok {
			continue
		}
		if err := replayer.Replay(ctx); err != nil {
			fmt.Fprintf(stderr, "check failed: %v\n", err)
			return 1
		}
	}
	fmt.Fprintf(stdout, "\n%d of %d relevant alerts from the last few days would be sent for %s. Nothing was posted.\n",
		wouldSend, total, cfg.Location)
	return 0
}

func when(a alert.Alert) string {
	if a.Issued.IsZero() {
		return "(no time)"
	}
	return a.Issued.In(time.FixedZone("JST", jstOffset)).Format("2006-01-02 15:04") + " JST"
}
