// Package clock provides the system time behind an interface, so business code never reads the clock itself and a
// test can hand it a fixed instant.
package clock

import "time"

// System reads the real time.
type System struct{}

func (System) Now() time.Time { return time.Now() }
