package alert

// Health is how a source is doing, for the light beside its line on the settings page.
type Health string

const (
	// Starting is not judged yet, such as while the first connection is made, so it raises no alarm.
	Starting Health = "starting"
	Up       Health = "up"
	Down     Health = "down"
)
