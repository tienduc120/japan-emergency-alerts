package main

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"go.uber.org/goleak"

	"github.com/tienduc120/japan-emergency-alerts/internal/config"
)

const token = "very-secret-token"

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func validEnv(key string) string {
	if key == "WEBHOOK_URL" {
		return "https://discord.com/api/webhooks/123/" + token
	}
	return ""
}

func envWith(webhook string) func(string) string {
	return func(key string) string {
		if key == "WEBHOOK_URL" {
			return webhook
		}
		return ""
	}
}

func TestOnlyAPersonsFailureHoldsTheWindowOpen(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
		code int
		want bool
	}{
		{"a failed start", nil, 1, true},
		{"a clean stop", nil, 0, false},
		{"a failed command", []string{"test"}, 1, true},
		{"the pop-up helper has nobody to read the message", []string{"popup-window"}, 1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := holdsWindowOpen(c.args, c.code); got != c.want {
				t.Errorf("holdsWindowOpen(%v, %d) = %v, want %v", c.args, c.code, got, c.want)
			}
		})
	}
}

func TestRunStopsCleanlyWhenItsContextEnds(t *testing.T) {
	t.Parallel()

	cfg, err := config.FromEnv(validEnv)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- run(ctx, logger, cfg, fakeSources(&fakeSource{})) }()

	cancel()

	if err = <-done; err != nil {
		t.Errorf("err = %v: a context that ends is a clean shutdown, not a failure", err)
	}
	for _, want := range []string{"started", "stopping"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, logs.String())
		}
	}
}

func TestTheCommandsThatCannotAskRefuseABadConfigurationAndSayWhy(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"test", "check"} {
		var stdout, stderr bytes.Buffer

		code := mainWith(t.Context(), []string{command}, func(string) string { return "" }, &stdout, &stderr)

		if code != exitUsage {
			t.Errorf("%s: exit code = %d, want %d", command, code, exitUsage)
		}
		if !strings.Contains(stderr.String(), "WEBHOOK_URL") || !strings.Contains(stderr.String(), "browser") {
			t.Errorf("%s: stderr must say what is missing and how to set it up:\n%s", command, stderr.String())
		}
		if stdout.Len() != 0 {
			t.Errorf("%s: nothing should be printed before the program starts:\n%s", command, stdout.String())
		}
	}
}

func TestTheProgramSaysWhatItWatchesButNeverTheWebhookToken(t *testing.T) {
	t.Parallel()
	h := newHarness(t, map[string]string{"WEBHOOK_URL": "https://discord.com/api/webhooks/123/" + token},
		fakeSources(&fakeSource{}))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	code := execute(ctx, nil, h.env)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr:\n%s", code, h.stderr.String())
	}
	if !strings.Contains(h.stdout.String(), "Tokyo") {
		t.Errorf("the output must show which location is being watched:\n%s", h.stdout.String())
	}
	if strings.Contains(h.stdout.String(), token) || strings.Contains(h.stderr.String(), token) {
		t.Error("the webhook token must never be written out")
	}
}

func TestTheTestCommandPostsOneMessageAndSaysSo(t *testing.T) {
	t.Parallel()
	var posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		posts.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	var stdout, stderr bytes.Buffer

	code := mainWith(t.Context(), []string{"test"}, envWith(srv.URL+"/hook/"+token), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr:\n%s", code, stderr.String())
	}
	if posts.Load() != 1 {
		t.Errorf("got %d posts, want exactly 1", posts.Load())
	}
	if !strings.Contains(stdout.String(), "sent") {
		t.Errorf("stdout must confirm the message was sent:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), token) || strings.Contains(stderr.String(), token) {
		t.Error("the webhook token must never be written out")
	}
}

func TestTheTestCommandReportsAFailureWithoutTheToken(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	var stdout, stderr bytes.Buffer

	code := mainWith(t.Context(), []string{"test"}, envWith(srv.URL+"/hook/"+token), &stdout, &stderr)

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "404") {
		t.Errorf("stderr must say what the webhook answered:\n%s", stderr.String())
	}
	if strings.Contains(stdout.String(), token) || strings.Contains(stderr.String(), token) {
		t.Error("the webhook token must never be written out")
	}
}

func TestAnUnknownCommandIsRefusedWithUsage(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"frobnicate"}, {"test", "extra"}} {
		var stdout, stderr bytes.Buffer

		code := mainWith(t.Context(), args, validEnv, &stdout, &stderr)

		if code != exitUsage || !strings.Contains(stderr.String(), "usage") {
			t.Errorf(
				"args %v: exit %d, stderr %q, want exit %d and a usage line",
				args,
				code,
				stderr.String(),
				exitUsage,
			)
		}
	}
}
