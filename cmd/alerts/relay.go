package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/tienduc120/japan-emergency-alerts/internal/alert"
	"github.com/tienduc120/japan-emergency-alerts/internal/clock"
	"github.com/tienduc120/japan-emergency-alerts/internal/filter"
	"github.com/tienduc120/japan-emergency-alerts/internal/web"
)

const maxTitleRunes = 100

type sender interface {
	Send(ctx context.Context, a alert.Alert) error
}

// multiSender tells a person in every way they asked for. The alert counts as delivered when any one way worked, so a
// dead webhook never costs the pop-up and a missing screen never costs the webhook; only when all fail is it an error
// that makes the source offer the alert again.
type multiSender struct {
	senders []sender
	logger  *slog.Logger
}

func (m multiSender) Send(ctx context.Context, a alert.Alert) error {
	if len(m.senders) == 0 {
		return errors.New("nobody can be told: there is neither a webhook nor a pop-up")
	}
	var errs []error
	for _, s := range m.senders {
		if err := s.Send(ctx, a); err != nil {
			errs = append(errs, err)
			m.logger.WarnContext(ctx, "one way of telling you failed", "id", a.ID, "error", err)
		}
	}
	if len(errs) == len(m.senders) {
		return errors.Join(errs...)
	}
	return nil
}

// relay is where every source's alerts meet: the filter decides, the sender posts, and the log says why anything was
// held back.
type relay struct {
	filter *filter.Filter
	sender sender
	logger *slog.Logger
	// record is told the ID of each alert that was sent, so a restart does not send it again. It may be nil.
	record func(id string)
	// activity is told what became of every alert, for the settings page's "Recent activity". It may be nil.
	activity func(web.Activity)
	// sent counts the alerts sent since the program started, for the heartbeat.
	sent atomic.Int64
}

// handle returns an error only when an alert should have been sent and could not be, so the source offers it again.
// Dropping an alert, for any reason, is not an error.
func (r *relay) handle(ctx context.Context, a alert.Alert) error {
	decision := r.filter.Decide(a)
	if decision != filter.Send {
		r.logger.InfoContext(ctx, "alert not sent", "id", a.ID, "kind", a.Kind.String(), "reason", decision.String())
		r.note(a, false, "Not sent: "+decision.String())
		return nil
	}
	if err := r.sender.Send(ctx, a); err != nil {
		// The filter already counted it as sent; without this the retry would be dropped as a duplicate.
		r.filter.Forget(a.ID)
		r.note(a, false, "Could not send: "+err.Error())
		return fmt.Errorf("send alert %s: %w", a.ID, err)
	}
	r.sent.Add(1)
	if r.record != nil {
		r.record(a.ID)
	}
	r.logger.InfoContext(ctx, "alert sent", "id", a.ID, "kind", a.Kind.String())
	r.note(a, true, "Sent")
	return nil
}

func (r *relay) note(a alert.Alert, sent bool, result string) {
	if r.activity == nil {
		return
	}
	when := a.Issued
	if when.IsZero() {
		when = clock.System{}.Now()
	}
	r.activity(web.Activity{
		When: when, Label: a.Kind.Label(), Title: firstLine(a.Text), Text: a.Text, Result: result, Sent: sent,
	})
}

// firstLine is the start of a text, short enough for a list.
func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	runes := []rune(line)
	if len(runes) > maxTitleRunes {
		return string(runes[:maxTitleRunes]) + "…"
	}
	return line
}
