//go:build !windows && !linux && !darwin

package clashsub

// Unknown Unix platforms deliberately refuse to claim ownership. This avoids
// terminating a recycled PID when no reliable executable-path API is available.
func ownedProcessRunning(int, string) bool { return false }
