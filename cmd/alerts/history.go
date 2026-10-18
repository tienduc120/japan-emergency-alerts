package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/tienduc120/japan-emergency-alerts/internal/clock"
	"github.com/tienduc120/japan-emergency-alerts/internal/state"
)

// saveEvery is how often the horizon is written. A crash loses at most this much, and the alerts of that minute are
// found again by catching up and recognised as already sent.
const saveEvery = time.Minute

// recordSent returns what the relay calls for each sent alert. A failure to write is logged once, not every time: the
// program keeps working, only without a memory across restarts.
func recordSent(store *state.Store, logger *slog.Logger) func(id string) {
	var once sync.Once
	return func(id string) {
		if err := store.MarkSent(id, clock.System{}.Now()); err != nil {
			once.Do(func() { logger.Warn("history could not be saved", "error", err) })
		}
	}
}

// keepHistory saves how far the sources have read, every minute and once more at shutdown.
func keepHistory(store *state.Store, sources []runner, logger *slog.Logger) func(context.Context) error {
	return func(ctx context.Context) error {
		ticker := time.NewTicker(saveEvery)
		defer ticker.Stop()
		warned := false
		save := func(ctx context.Context) {
			horizon := earliestHorizon(sources)
			if horizon.IsZero() || horizon.Equal(store.Horizon()) {
				return
			}
			if err := store.SetHorizon(horizon); err != nil && !warned {
				warned = true
				logger.WarnContext(ctx, "history could not be saved", "error", err)
			}
		}
		for {
			select {
			case <-ctx.Done():
				save(context.WithoutCancel(ctx))
				return nil
			case <-ticker.C:
				save(ctx)
			}
		}
	}
}

// earliestHorizon is the oldest of the sources' horizons, because catching up must start from the source that has read
// the least. Sources that have not read anything yet, or have no history, are left out.
func earliestHorizon(sources []runner) time.Time {
	var earliest time.Time
	for _, source := range sources {
		h, ok := source.(horizoner)
		if !ok {
			continue
		}
		if t := h.Horizon(); !t.IsZero() && (earliest.IsZero() || t.Before(earliest)) {
			earliest = t
		}
	}
	return earliest
}
