// Package notice is the short notification shown when Windows starts the program at sign-in: it says the program is
// running, fades by itself, and opens the settings page when clicked. A helper process shows it, so the program never
// waits on it.
package notice

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf16"
)

// Command is the command the program is started with, by itself, to show the notification.
const Command = "start-notice"

const (
	// Windows keeps 64 and 256 UTF-16 units for a notification's title and text, the last for the terminating zero.
	maxTitle = 63
	maxText  = 255
	ellipsis = "…"
)

// Message is one notification. Address is the settings page, opened on a click and never shown.
type Message struct {
	Title   string `json:"title"`
	Text    string `json:"text"`
	Address string `json:"address"`
}

// Fit shortens the title and text to what Windows shows, so they end in "…" instead of being cut mid-character.
func Fit(m Message) Message {
	m.Title, m.Text = fit(m.Title, maxTitle), fit(m.Text, maxText)
	return m
}

func fit(s string, limit int) string {
	if len(utf16.Encode([]rune(s))) <= limit {
		return s
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		width := utf16.RuneLen(r)
		if width < 0 {
			width = 1
		}
		if used+width > limit-len(utf16.Encode([]rune(ellipsis))) {
			break
		}
		b.WriteRune(r)
		used += width
	}
	return b.String() + ellipsis
}

// Encode writes m as one line, for the helper's input.
func Encode(w io.Writer, m Message) error {
	if err := json.NewEncoder(w).Encode(m); err != nil {
		return fmt.Errorf("write the notification: %w", err)
	}
	return nil
}

// Decode reads the line Encode wrote.
func Decode(r *bufio.Reader) (Message, error) {
	line, err := r.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return Message{}, fmt.Errorf("read the notification: %w", err)
	}
	var m Message
	if err = json.Unmarshal(line, &m); err != nil {
		return Message{}, fmt.Errorf("read the notification: %w", err)
	}
	return m, nil
}

// Event is what happened to a notification.
type Event int

const (
	// Clicked means the person clicked it.
	Clicked Event = iota + 1
	// Closed means it went away by itself, was dismissed, or the program stopped.
	Closed
)

// Tray is where the notification is shown.
type Tray interface {
	// Show adds the icon and its notification. It fails while the taskbar is not there yet.
	Show(m Message) error
	Events() <-chan Event
	// Close removes the icon.
	Close() error
}
