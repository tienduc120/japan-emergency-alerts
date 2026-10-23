//go:build !windows

package main

// liveStartup is nil where there is no start-with-Windows entry.
func liveStartup() startupEntry { return nil }
