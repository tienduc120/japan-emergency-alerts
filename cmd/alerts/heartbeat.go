package main

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/tienduc120/japan-emergency-alerts/internal/config"
)

const jstZoneOffset = 9 * 60 * 60

type statusSender interface {
	Status(ctx context.Context, text string) error
}

// heartbeat posts a status message every so often. It is how a silent death is noticed: the message stops. It also says
// whether each source is healthy, so a program that is running but blind says so.
func heartbeat(
	every time.Duration,
	sender statusSender,
	report func() string,
	logger *slog.Logger,
) func(context.Context) error {
	return func(ctx context.Context) error {
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				// One missed heartbeat is not worth retrying: the next one is the retry.
				if err := sender.Status(ctx, report()); err != nil && ctx.Err() == nil {
					logger.WarnContext(ctx, "heartbeat not delivered", "error", err)
				}
			}
		}
	}
}

func statusText(cfg config.Config, started time.Time, sent int64, sources []runner) string {
	lines := []string{
		"japan-emergency-alerts is running (since " + inJST(started) + ").",
		"Watching: " + cfg.Location.String() + " (earthquake reports from " + cfg.MinIntensity.Japanese() + ").",
		"Alerts sent since then: " + strconv.FormatInt(sent, 10) + ".",
	}
	for _, source := range sources {
		if s, ok := source.(statuser); ok {
			if line := s.Status(); line != "" {
				lines = append(lines, line)
			}
		}
	}
	lines = append(lines, "This is a status message, not an alert.")
	return strings.Join(lines, "\n")
}

func inJST(t time.Time) string {
	return t.In(time.FixedZone("JST", jstZoneOffset)).Format("2006-01-02 15:04") + " JST"
}
