// Package state keeps the little the program must remember across a restart: when it last read JMA's feeds in full, and
// which alerts it has already sent. Without it a restart would either miss what happened while the program was off or
// send the same alert twice.
package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"sync"
	"time"

	"github.com/tienduc120/japan-emergency-alerts/internal/fileutil"
)

const (
	version = 1
	// keepFor matches how long the filter remembers a sent alert; remembering longer would only grow the file.
	keepFor = 24 * time.Hour
)

type fileContents struct {
	Version int                  `json:"version"`
	Horizon time.Time            `json:"horizon"`
	Sent    map[string]time.Time `json:"sent"`
}

// Store is safe for several goroutines. A Store with no path keeps everything in memory only.
type Store struct {
	path string

	mu      sync.Mutex
	horizon time.Time
	sent    map[string]time.Time
}

// Open reads the file at path. A missing file is a first run, not an error. An unreadable one gives a usable empty
// Store together with an error to log, because losing the history must not stop the program from running.
func Open(path string) (*Store, error) {
	s := &Store{path: path, sent: map[string]time.Time{}}
	if path == "" {
		return s, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("read state file: %w", err)
	}
	var c fileContents
	// A byte-order mark is not JSON, and Windows editors add one without being asked.
	if err = json.Unmarshal(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), &c); err != nil {
		return s, fmt.Errorf("state file %s is damaged and will be replaced: %w", path, err)
	}
	s.horizon = c.Horizon
	maps.Copy(s.sent, c.Sent)
	return s, nil
}

// Horizon is when the feeds were last read in full, or zero on a first run.
func (s *Store) Horizon() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.horizon
}

// SetHorizon records it and saves.
func (s *Store) SetHorizon(t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.horizon = t
	return s.save()
}

// Sent is a copy of the remembered alerts and when each was sent.
func (s *Store) Sent() map[string]time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.sent)
}

// MarkSent records a sent alert, forgets those more than a day older, and saves.
func (s *Store) MarkSent(id string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent[id] = at
	for other, sentAt := range s.sent {
		if at.Sub(sentAt) >= keepFor {
			delete(s.sent, other)
		}
	}
	return s.save()
}

// save writes the whole file in one atomic step, so a crash can never leave half of it. The caller holds the lock.
func (s *Store) save() error {
	if s.path == "" {
		return nil
	}
	data, err := json.Marshal(fileContents{Version: version, Horizon: s.horizon, Sent: s.sent})
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	if err = fileutil.WriteAtomic(s.path, data); err != nil {
		return fmt.Errorf("save state file: %w", err)
	}
	return nil
}
