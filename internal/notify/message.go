package notify

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tienduc120/japan-emergency-alerts/internal/alert"
)

const (
	source = "Japan Meteorological Agency"
	// notice is the disclaimer the JMA terms and plain honesty ask for: the text is JMA's, the wrapping is ours.
	notice = "Unofficial relay by japan-emergency-alerts, not affiliated with JMA. Do not rely on it alone."
	// testText says it is not an alert, so nobody in the channel mistakes it for one.
	testText = "Test message from japan-emergency-alerts. If you can read this, your webhook works. This is not an alert."

	// discordLimit is Discord's cap on the characters of one message.
	discordLimit = 2000
	issuedLayout = "2006-01-02 15:04"
	jstOffset    = 9 * 60 * 60
)

// render is the human-readable message: a heading, JMA's text exactly as received, and the credit.
func render(a alert.Alert) string {
	var b strings.Builder
	b.WriteString("**" + a.Kind.Label() + "**")
	if !a.Issued.IsZero() {
		// A fixed zone, because a machine without timezone data must still show the right time.
		b.WriteString(" | " + a.Issued.In(time.FixedZone("JST", jstOffset)).Format(issuedLayout) + " JST")
	}
	if a.Late {
		// No reason is given: a late alert can follow a retry or a delay at JMA as well as time spent offline.
		b.WriteString(" | sent late")
	}
	b.WriteString("\n" + a.Text + "\n\nSource: " + source + ". " + notice)
	return b.String()
}

type allowedMentions struct {
	// Parse is an empty list, not nil, so the field is sent and nothing in the text can ping anyone.
	Parse []string `json:"parse"`
}

type discordPayload struct {
	Content         string          `json:"content"`
	AllowedMentions allowedMentions `json:"allowed_mentions"`
}

func discordBodies(message string) ([][]byte, error) {
	parts := split(message, discordLimit)
	bodies := make([][]byte, 0, len(parts))
	for _, part := range parts {
		body, err := json.Marshal(discordPayload{Content: part, AllowedMentions: allowedMentions{Parse: []string{}}})
		if err != nil {
			return nil, fmt.Errorf("encode discord message: %w", err)
		}
		bodies = append(bodies, body)
	}
	return bodies, nil
}

type jsonArea struct {
	Prefecture int    `json:"prefecture"`
	Intensity  string `json:"intensity,omitempty"`
}

type jsonPayload struct {
	ID      string     `json:"id"`
	Kind    string     `json:"kind"`
	Issued  time.Time  `json:"issued,omitzero"`
	Late    bool       `json:"late,omitempty"`
	Areas   []jsonArea `json:"areas"`
	Text    string     `json:"text"`
	Source  string     `json:"source"`
	Notice  string     `json:"notice"`
	Message string     `json:"message"`
}

func jsonBody(a alert.Alert) ([]byte, error) {
	areas := make([]jsonArea, 0, len(a.Areas))
	for _, area := range a.Areas {
		out := jsonArea{Prefecture: area.Prefecture}
		// Only an earthquake report measures shaking; a zero on any other alert would read as "no shaking".
		if a.Kind == alert.KindEarthquakeReport {
			out.Intensity = area.Intensity.String()
		}
		areas = append(areas, out)
	}
	body, err := json.Marshal(jsonPayload{
		ID: a.ID, Kind: a.Kind.String(), Issued: a.Issued, Late: a.Late, Areas: areas, Text: a.Text,
		Source: source, Notice: notice, Message: render(a),
	})
	if err != nil {
		return nil, fmt.Errorf("encode webhook message: %w", err)
	}
	return body, nil
}

// split cuts a message into parts of at most limit characters, only at line breaks, so rejoining the parts with a
// line break gives back the original. A single line longer than the limit is cut at the limit, the one case where a
// part boundary falls inside a line.
func split(message string, limit int) []string {
	if utf8.RuneCountInString(message) <= limit {
		return []string{message}
	}
	var (
		parts []string
		lines []string
		runes int
	)
	flush := func() {
		parts = append(parts, strings.Join(lines, "\n"))
		lines, runes = nil, 0
	}
	for line := range strings.SplitSeq(message, "\n") {
		for _, piece := range chop(line, limit) {
			cost := utf8.RuneCountInString(piece)
			if len(lines) > 0 {
				cost++
			}
			if runes+cost > limit {
				flush()
				cost = utf8.RuneCountInString(piece)
			}
			lines = append(lines, piece)
			runes += cost
		}
	}
	flush()
	return parts
}

func chop(line string, limit int) []string {
	runes := []rune(line)
	if len(runes) <= limit {
		return []string{line}
	}
	var pieces []string
	for len(runes) > limit {
		pieces = append(pieces, string(runes[:limit]))
		runes = runes[limit:]
	}
	return append(pieces, string(runes))
}
