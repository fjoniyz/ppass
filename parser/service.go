package parser

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/vishvananda/netlink"

	"private_paas/network"
)

func (s *Service) execPythonService(cmd *exec.Cmd, bridge *netlink.Bridge, ip string) {
	// Write logs to a file instead of inheriting parent's stdout/stderr
	logFile, err := os.OpenFile(
		fmt.Sprintf("/var/log/%s.log", s.Name),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0o644,
	)
	if err != nil {
		log.Fatalf("Failed to open log file: %v", err)
	}

	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	// Create a new network namespace for the process
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:     true,
		Cloneflags: syscall.CLONE_NEWNET,
	}

	if err := cmd.Start(); err != nil {
		log.Fatalf("cmd.Start() failed: %s\n", err)
	}

	pid := cmd.Process.Pid
	s.Pid = pid

	// Wait for the process to be available in /proc
	maxRetries := 10
	for i := 0; i < maxRetries; i++ {
		if _, err := os.Stat(fmt.Sprintf("/proc/%d/ns/net", pid)); err == nil {
			break
		}
		if i == maxRetries-1 {
			log.Fatalf("Process %d network namespace not available after timeout", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}

	fmt.Printf("Service %s started in background (PID: %d)\n", s.Name, pid)
	fmt.Printf("Logs available at /var/log/%s.log\n", s.Name)

	s.cg(pid)
	// Close our handle to the log file — the child keeps its own fd open
	logFile.Close()

	if s.Lb {
		s.ConnectToLb(bridge, ip)
	}

	if err := cmd.Process.Release(); err != nil {
		log.Fatalf("Failed to release process: %v", err)
	}
}

// cg places the process into a cgroup
func (s *Service) cg(pid int) {
	cgroupName := strconv.Itoa(pid)

	v2Path := filepath.Join("/sys/fs/cgroup", cgroupName)
	if err := os.MkdirAll(v2Path, 0o755); err == nil {
		slog.Info("Process added to cgroup v2", "pid", pid, "cgroup", v2Path)
		lim := s.Limitations

		if lim.Memory != "" {
			if err := os.WriteFile(filepath.Join(v2Path, "memory.max"), []byte(s.Limitations.Memory), 0o644); err != nil {
				slog.Error("Failed to set memory limit", "error", err)
			}
		}

		if lim.CPU != "" {
			if err := os.WriteFile(filepath.Join(v2Path, "cpu.max"), []byte(s.Limitations.CPU), 0o644); err != nil {
				slog.Error("Failed to set CPU limit", "error", err)
			}
		}

		if lim.Pids != "" {
			if err := os.WriteFile(filepath.Join(v2Path, "pids.max"), []byte(s.Limitations.Pids), 0o644); err != nil {
				slog.Error("Failed to set PIDs limit", "error", err)
			}
		}
		return
	}
}

// ConnectToLb connects the service process to the load balancer bridge
func (s *Service) ConnectToLb(bridge *netlink.Bridge, ip string) {
	slog.Info("Connecting service to bridge", "service", s.Name, "bridge", bridge.Attrs().Name)

	err := network.ConnectProcessToBridge(s.Pid, bridge, ip, fmt.Sprintf("v-%s", s.Name))
	if err != nil {
		slog.Error("Failed to connect service to bridge", "error", err)
	} else {
		slog.Info("Successfully connected service to bridge", "service", s.Name, "ip", ip)
	}
}

func (s *Service) CreateService(body string, bridge *netlink.Bridge, ip string) {
	// Populate the service struct from the YAML body
	s.ParseService(body)

	slog.Info("Creating service", "name", s.Name, "technology", s.Technology, "load_balanced", s.Lb)

	switch s.Technology {
	case Python:
		slog.Info("Executing Python service", "name", s.Name)
		if s.Lb {
			slog.Info("Adding the service to the load balancer", "name", s.Name)
			if len(s.LbConfig.Servers) == 0 {
				s.LbConfig.Servers = []string{fmt.Sprintf("%s:8888", ip)}
			}
			UpdateLbConfig(s.LbConfig)
		}
		cmd := exec.Command("python3", s.Path)
		s.execPythonService(cmd, bridge, ip)

	default:
		slog.Warn("Unsupported technology", "technology", s.Technology)
	}
}
