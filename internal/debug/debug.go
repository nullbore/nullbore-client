package debug

import "log"

// Enabled controls whether debug-level logging is active.
// Set via --debug or -v flag.
var Enabled bool

// Printf logs a message only when debug mode is enabled.
func Printf(format string, args ...interface{}) {
	if Enabled {
		log.Printf("[debug] "+format, args...)
	}
}
