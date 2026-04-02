//go:build !windows

package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/nullbore/nullbore-client/internal/config"
)

// detachDaemon re-execs the current binary as a background daemon process.
func detachDaemon(pidPath, logPath string) error {
	// Check if already running
	if pid, err := readPID(pidPath); err == nil {
		if process, err := os.FindProcess(pid); err == nil {
			if err := process.Signal(syscall.Signal(0)); err == nil {
				return fmt.Errorf("daemon already running (PID %d). Use 'nullbore daemon --stop' first", pid)
			}
		}
	}

	// Open log file
	configDir := config.ConfigDir()
	os.MkdirAll(configDir, 0700)

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("opening log file: %w", err)
	}

	// Re-exec ourselves with just "daemon" (no --detach) as a detached process
	exe, err := os.Executable()
	if err != nil {
		logFile.Close()
		return fmt.Errorf("finding executable: %w", err)
	}

	attr := &os.ProcAttr{
		Dir: "/",
		Env: os.Environ(),
		Files: []*os.File{
			os.Stdin, // stdin (will be /dev/null effectively)
			logFile,  // stdout → log
			logFile,  // stderr → log
		},
		Sys: &syscall.SysProcAttr{
			Setsid: true, // new session — fully detached
		},
	}

	proc, err := os.StartProcess(exe, []string{exe, "daemon"}, attr)
	if err != nil {
		logFile.Close()
		return fmt.Errorf("starting daemon: %w", err)
	}
	logFile.Close()

	// Write PID file
	os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", proc.Pid)), 0644)

	fmt.Printf("Daemon started (PID %d)\n", proc.Pid)
	fmt.Printf("  Log: %s\n", logPath)
	fmt.Printf("  Stop: nullbore daemon --stop\n")

	// Release the child — we don't wait for it
	proc.Release()
	return nil
}

// stopDaemon reads the PID file and sends SIGTERM to the daemon.
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

	// Check if process is alive
	if err := process.Signal(syscall.Signal(0)); err != nil {
		os.Remove(pidPath)
		return fmt.Errorf("daemon (PID %d) is not running (stale PID file removed)", pid)
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("sending signal to daemon: %w", err)
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
