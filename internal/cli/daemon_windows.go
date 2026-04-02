//go:build windows

package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// detachDaemon is not supported on Windows.
// Use a Windows Service or Task Scheduler instead.
func detachDaemon(pidPath, logPath string) error {
	return fmt.Errorf("--detach is not supported on Windows\n\n  Use one of:\n    - Windows Task Scheduler\n    - nssm (Non-Sucking Service Manager)\n    - Run in a terminal: nullbore daemon")
}

// stopDaemon reads the PID file and terminates the daemon on Windows.
func stopDaemon(pidPath string) error {
	pid, err := readPID(pidPath)
	if err != nil {
		return fmt.Errorf("no daemon PID file found — is a daemon running?")
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		os.Remove(pidPath)
		return fmt.Errorf("process %d not found", pid)
	}

	if err := process.Kill(); err != nil {
		os.Remove(pidPath)
		return fmt.Errorf("killing daemon (PID %d): %w", pid, err)
	}

	os.Remove(pidPath)
	fmt.Printf("Daemon (PID %d) stopped.\n", pid)
	return nil
}

// readPID reads a PID from a file.
func readPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}
