package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tienduc120/japan-emergency-alerts/internal/alert"
	"github.com/tienduc120/japan-emergency-alerts/internal/clock"
	"github.com/tienduc120/japan-emergency-alerts/internal/filter"
	"github.com/tienduc120/japan-emergency-alerts/internal/intensity"
	"github.com/tienduc120/japan-emergency-alerts/internal/region"
)

type fakeSender struct {
	mu       sync.Mutex
	sent     []string
	failNext int
}

func (s *fakeSender) Send(_ context.Context, a alert.Alert) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failNext > 0 {
		s.failNext--
		return errors.New("webhook down")
	}
	s.sent = append(s.sent, a.ID)
	return nil
}

func (s *fakeSender) ids() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.sent...)
}

func newRelay(t *testing.T, sender *fakeSender, logs *bytes.Buffer) *relay {
	t.Helper()
	tokyo, ok := region.Lookup("Tokyo")
	if !ok {
		t.Fatal("Tokyo must exist")
	}
	return &relay{
		filter: filter.New(region.InPrefecture(tokyo), intensity.FiveLower, clock.System{}),
		sender: sender,
		logger: slog.New(slog.NewJSONHandler(logs, nil)),
	}
}

func tsunamiFor(prefecture int, id string) alert.Alert {
	return alert.Alert{
		ID:     id,
		Kind:   alert.KindTsunami,
		Issued: time.Date(2026, time.October, 18, 3, 0, 0, 0, time.UTC),
		Areas:  []alert.Area{{Prefecture: prefecture}},
		Text:   "津波警報",
	}
}

func TestARelevantAlertIsSentOnceAndARepeatIsNot(t *testing.T) {
	t.Parallel()
	sender := &fakeSender{}
	r := newRelay(t, sender, &bytes.Buffer{})

	first := r.handle(t.Context(), tsunamiFor(13, "a"))
	second := r.handle(t.Context(), tsunamiFor(13, "a"))

	if first != nil || second != nil {
		t.Errorf("errors %v, %v, want none: a duplicate is not a failure", first, second)
	}
	if got := sender.ids(); len(got) != 1 || got[0] != "a" {
		t.Errorf("sent %v, want [a] once", got)
	}
}

func TestAnAlertForElsewhereIsNotSentAndIsNotAnError(t *testing.T) {
	t.Parallel()
	sender := &fakeSender{}
	var logs bytes.Buffer
	r := newRelay(t, sender, &logs)

	err := r.handle(t.Context(), tsunamiFor(1, "far"))

	if err != nil || len(sender.ids()) != 0 {
		t.Errorf("err %v, sent %v, want neither", err, sender.ids())
	}
	for _, want := range []string{"far", "outside the watched location", "tsunami"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log lacks %q, so nobody could tell why the alert was not sent:\n%s", want, logs.String())
		}
	}
}

func TestAFailedSendIsReportedAndTheAlertCanBeOfferedAgain(t *testing.T) {
	t.Parallel()
	sender := &fakeSender{failNext: 1}
	r := newRelay(t, sender, &bytes.Buffer{})

	first := r.handle(t.Context(), tsunamiFor(13, "a"))
	second := r.handle(t.Context(), tsunamiFor(13, "a"))

	if first == nil || !strings.Contains(first.Error(), "a") {
		t.Errorf("first = %v, want an error naming the alert", first)
	}
	if second != nil {
		t.Errorf("second = %v, want success: a failed send must not be remembered as sent", second)
	}
	if got := sender.ids(); len(got) != 1 {
		t.Errorf("sent %v, want the alert sent once, on the second try", got)
	}
}

func TestAnAlertCountsAsDeliveredWhenAnyChannelCarriedIt(t *testing.T) {
	t.Parallel()
	down := &fakeSender{failNext: 1}
	up := &fakeSender{}
	var logs bytes.Buffer
	both := multiSender{senders: []sender{down, up}, logger: slog.New(slog.NewJSONHandler(&logs, nil))}

	err := both.Send(t.Context(), tsunamiFor(13, "a"))

	if err != nil || len(up.ids()) != 1 {
		t.Errorf("err %v, delivered %v, want the working channel to carry it and no error", err, up.ids())
	}
	if !strings.Contains(logs.String(), "webhook down") {
		t.Errorf("the channel that failed must be logged, or nobody learns it is broken:\n%s", logs.String())
	}
}

func TestAnAlertIsLostOnlyWhenEveryChannelFails(t *testing.T) {
	t.Parallel()
	both := multiSender{
		senders: []sender{&fakeSender{failNext: 1}, &fakeSender{failNext: 1}},
		logger:  slog.New(slog.DiscardHandler),
	}

	err := both.Send(t.Context(), tsunamiFor(13, "a"))

	if err == nil {
		t.Error("want an error so the source offers the alert again")
	}
}

func TestWithNoChannelAtAllSendingIsAnError(t *testing.T) {
	t.Parallel()

	if err := (multiSender{logger: slog.New(slog.DiscardHandler)}).Send(t.Context(), tsunamiFor(13, "a")); err == nil {
		t.Error("want an error: an alert nobody can receive must not look delivered")
	}
}
