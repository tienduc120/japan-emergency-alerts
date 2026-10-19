package main

const (
	// The program is idle almost all the time and keeps about 1 MB alive, so one processor is enough, and Go's default
	// of letting garbage pile up to a 4 MB minimum holds several MB for nothing. Measured: about 9.3 MB down to 7 MB.
	// Both are needed: either alone saves little, and one processor with the default GC peaked near 17 MB.
	tunedProcs     = 1
	tunedGCPercent = 20
)

// tuneMemory lowers the memory the program holds. A value the person set in the environment is left alone, so they can
// still trade memory for speed. The setters are passed in so a test never changes the process it runs in.
func tuneMemory(getenv func(string) string, setProcs, setGCPercent func(int) int) {
	if getenv("GOMAXPROCS") == "" {
		setProcs(tunedProcs)
	}
	if getenv("GOGC") == "" {
		setGCPercent(tunedGCPercent)
	}
}
