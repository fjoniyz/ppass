package utils

import (
	"os"
	"testing"
)

func TestFindPidForEnvoyNoFile(t *testing.T) {
	// Clean up file if exists
	_ = os.Remove("/run/envoy-paas.pid")

	pid, err := FindPidForEnvoy()
	if err != nil {
		t.Fatalf("expected no error when file is missing, got: %v", err)
	}
	if pid != 0 {
		t.Errorf("expected pid 0 when file is missing, got: %d", pid)
	}
}

func TestFindPidForEnvoyCurrentProcess(t *testing.T) {
	// If running with permissions to write to /run, test with current PID
	currPid := os.Getpid()
	err := os.WriteFile("/run/envoy-paas.pid", []byte(string(rune(currPid))), 0o644)
	if err != nil {
		// Skip if no permission to write to /run without sudo
		t.Skip("skipping /run write test due to permissions")
	}
	defer os.Remove("/run/envoy-paas.pid")
}
