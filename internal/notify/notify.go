// Package notify posts alerts to a webhook, as a Discord message or as structured JSON, and retries when the receiver
// is busy or briefly down.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tienduc120/japan-emergency-alerts/internal/alert"
)

// Format is what the receiver expects in the body.
type Format int

const (
	FormatDiscord Format = iota + 1 // {"content": "..."}, split to fit Discord's message size
	FormatJSON                      // one structured JSON object per alert, for anything else
)

const (
	maxAttempts    = 3
	defaultBackoff = time.Second
	// maxRetryAfter caps how long a receiver can ask us to wait: an alert that arrives minutes late has lost its point.
	maxRetryAfter  = 30 * time.Second
	requestTimeout = 10 * time.Second
	maxErrorBody   = 200
	noHint         = time.Duration(-1)
	userAgent      = "japan-emergency-alerts"
)

// FormatFor picks Discord for Discord's own hosts and plain JSON for everything else. The host must match exactly or be
// a subdomain, so "notdiscord.com" and "discord.com.example" do not count.
func FormatFor(webhook *url.URL) Format {
	host := strings.ToLower(webhook.Hostname())
	for _, domain := range []string{"discord.com", "discordapp.com"} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return FormatDiscord
		}
	}
	return FormatJSON
}

// Options tune a Sender; the zero value is right for real use, and tests shrink the backoff.
type Options struct {
	// Client defaults to one with a timeout and no connection reuse: alerts are rare, so an idle connection kept open
	// would only hold a socket.
	Client *http.Client
	// Backoff is the wait before the first retry, doubling after each; it defaults to one second.
	Backoff time.Duration
}

// Sender posts to one webhook. Its URL carries a secret, so the Sender never prints it and strips it from errors.
type Sender struct {
	webhook string
	format  Format
	client  *http.Client
	backoff time.Duration
}

func New(webhook *url.URL, format Format, opts Options) *Sender {
	client := opts.Client
	if client == nil {
		client = &http.Client{
			Timeout: requestTimeout,
			Transport: &http.Transport{
				Proxy:               http.ProxyFromEnvironment,
				DisableKeepAlives:   true,
				TLSHandshakeTimeout: requestTimeout,
			},
		}
	}
	backoff := opts.Backoff
	if backoff <= 0 {
		backoff = defaultBackoff
	}
	return &Sender{webhook: webhook.String(), format: format, client: client, backoff: backoff}
}

// Send posts one alert, with JMA's text unchanged.
func (s *Sender) Send(ctx context.Context, a alert.Alert) error {
	switch s.format {
	case FormatDiscord:
		bodies, err := discordBodies(render(a))
		if err != nil {
			return err
		}
		for _, body := range bodies {
			if err = s.post(ctx, body); err != nil {
				return err
			}
		}
		return nil
	case FormatJSON:
		body, err := jsonBody(a)
		if err != nil {
			return err
		}
		return s.post(ctx, body)
	default:
		return fmt.Errorf("unknown webhook format %d", s.format)
	}
}

// Test posts a message that says it is not an alert, to check that the webhook works.
func (s *Sender) Test(ctx context.Context) error {
	return s.notice(ctx, "test", testText)
}

// Status posts a line about the program itself, such as the daily heartbeat. It is the program's own words, so it gets
// none of the alert wrapping.
func (s *Sender) Status(ctx context.Context, text string) error {
	return s.notice(ctx, "status", text)
}

// notice posts the program's own text. For a structured receiver the key says what kind of notice it is.
func (s *Sender) notice(ctx context.Context, key, text string) error {
	switch s.format {
	case FormatDiscord:
		bodies, err := discordBodies(text)
		if err != nil {
			return err
		}
		for _, body := range bodies {
			if err = s.post(ctx, body); err != nil {
				return err
			}
		}
		return nil
	case FormatJSON:
		body, err := json.Marshal(map[string]any{key: true, "message": text})
		if err != nil {
			return fmt.Errorf("encode %s message: %w", key, err)
		}
		return s.post(ctx, body)
	default:
		return fmt.Errorf("unknown webhook format %d", s.format)
	}
}

// outcome is what one attempt learned: whether to try again, and how long the receiver asked us to wait.
type outcome struct {
	err   error
	retry bool
	wait  time.Duration
}

func (s *Sender) post(ctx context.Context, body []byte) error {
	delay := s.backoff
	for attempt := 1; ; attempt++ {
		result := s.attempt(ctx, body)
		if result.err == nil {
			return nil
		}
		if !result.retry || attempt == maxAttempts {
			return result.err
		}
		wait := result.wait
		if wait == noHint {
			wait = delay
			delay *= 2
		}
		if err := sleep(ctx, wait); err != nil {
			return err
		}
	}
}

func (s *Sender) attempt(ctx context.Context, body []byte) outcome {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.webhook, bytes.NewReader(body))
	if err != nil {
		return outcome{err: scrub(err), wait: noHint}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		// A cancelled context is the caller asking us to stop, so it is never worth another try.
		return outcome{err: scrub(err), retry: ctx.Err() == nil, wait: noHint}
	}
	defer func() { _ = resp.Body.Close() }()
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))

	switch {
	case resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices:
		return outcome{}
	case resp.StatusCode == http.StatusTooManyRequests:
		return outcome{err: statusError(resp.StatusCode, snippet), retry: true, wait: retryAfter(resp)}
	case resp.StatusCode >= http.StatusInternalServerError:
		return outcome{err: statusError(resp.StatusCode, snippet), retry: true, wait: noHint}
	default:
		return outcome{err: statusError(resp.StatusCode, snippet), wait: noHint}
	}
}

func statusError(code int, body []byte) error {
	return fmt.Errorf("webhook answered %d %s: %s", code, http.StatusText(code), strings.TrimSpace(string(body)))
}

// retryAfter reads the wait a receiver asks for, in seconds, or noHint when it gave none we can read.
func retryAfter(resp *http.Response) time.Duration {
	seconds, err := strconv.ParseFloat(resp.Header.Get("Retry-After"), 64)
	if err != nil || seconds < 0 {
		return noHint
	}
	return min(time.Duration(seconds*float64(time.Second)), maxRetryAfter)
}

// sleep waits for d or until ctx ends, whichever comes first.
func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// scrub drops the URL from a transport error: net/http quotes the full request URL, and ours holds the webhook token.
func scrub(err error) error {
	if urlErr, ok := errors.AsType[*url.Error](err); ok {
		return fmt.Errorf("%s: %w", urlErr.Op, urlErr.Err)
	}
	return err
}
