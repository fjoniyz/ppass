package utils

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

func FindPidForEnvoy() (int, error) {
	data, err := os.ReadFile("/run/envoy-paas.pid")
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("failed to read envoy-paas.pid: %v", err)
	}

	pidStr := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return 0, fmt.Errorf("invalid PID in envoy-paas.pid: %v", err)
	}

	// Verify if the process is actually running
	process, err := os.FindProcess(pid)
	if err != nil {
		return 0, nil
	}

	// Send signal 0 to check if the process is alive
	err = process.Signal(syscall.Signal(0))
	if err != nil {
		return 0, nil // Process not running
	}

	return pid, nil
}
