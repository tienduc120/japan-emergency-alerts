// Package jma reads JMA's public Atom feeds and XML telegrams and turns the ones worth relaying into alerts.
package jma

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"time"
)

// fileNameParts is how many underscore-separated parts a telegram's file name has: time, a serial, type, area code.
const fileNameParts = 4

// Entry is one line of a feed: a telegram JMA has published, with JMA's own one-paragraph summary.
type Entry struct {
	ID      string // the address of the telegram's XML
	Title   string
	Content string
	Updated time.Time
}

type atomFeed struct {
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title   string `xml:"title"`
	ID      string `xml:"id"`
	Updated string `xml:"updated"`
	Content string `xml:"content"`
}

// ParseFeed reads an Atom feed.
func ParseFeed(r io.Reader) ([]Entry, error) {
	var feed atomFeed
	if err := xml.NewDecoder(r).Decode(&feed); err != nil {
		return nil, fmt.Errorf("read feed: %w", err)
	}
	entries := make([]Entry, 0, len(feed.Entries))
	for _, e := range feed.Entries {
		// An unreadable time leaves Updated zero; the entry is still worth reading.
		updated, _ := time.Parse(time.RFC3339, strings.TrimSpace(e.Updated))
		entries = append(entries, Entry{
			ID:      strings.TrimSpace(e.ID),
			Title:   strings.TrimSpace(e.Title),
			Content: strings.TrimSpace(e.Content),
			Updated: updated,
		})
	}
	return entries, nil
}

// Name is the telegram's file name without ".xml". It is unique to the telegram, so it identifies the alert.
func (e Entry) Name() string {
	u, err := url.Parse(e.ID)
	if err != nil {
		return ""
	}
	name, ok := strings.CutSuffix(path.Base(u.Path), ".xml")
	if !ok {
		return ""
	}
	return name
}

// Type is JMA's telegram type code, such as VXSE53 for an earthquake and seismic intensity report. It is read from the
// file name because that is stable, unlike the Japanese title. An odd name gives "".
func (e Entry) Type() string {
	parts := strings.Split(e.Name(), "_")
	if len(parts) != fileNameParts {
		return ""
	}
	return parts[2]
}

// AreaCode is the last part of the file name: the forecast area or office the telegram belongs to.
func (e Entry) AreaCode() string {
	parts := strings.Split(e.Name(), "_")
	if len(parts) != fileNameParts {
		return ""
	}
	return parts[3]
}
