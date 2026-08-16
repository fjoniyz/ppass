package parser

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"private_paas/network"
)

func (d *Database) ConnectToBridge(ip string) {
	bridge := network.CreateBridge()
	pid, _ := strconv.Atoi(d.Pid)

	slog.Info("Connecting database to bridge", "database", d.Name, "bridge", bridge.Attrs().Name)

	err := network.ConnectProcessToBridge(pid, bridge, ip, fmt.Sprintf("v-%s", d.Name))
	if err != nil {
		slog.Error("Failed to connect database to bridge", "error", err)
	} else {
		slog.Info("Successfully connected database to bridge", "database", d.Name, "ip", ip)
	}
}

func (d *Database) execPostgreSQL() {
	namespaceName := fmt.Sprintf("ns-%s", d.Name)

	// Create a new network namespace
	if err := exec.Command("ip", "netns", "add", namespaceName).Run(); err != nil {
		// If the namespace already exists, we can ignore the error.
		if !strings.Contains(err.Error(), "File exists") {
			log.Fatalf("Failed to create network namespace: %v", err)
		}
	}

	cmd := exec.Command("postgres", "-D", fmt.Sprintf("/var/lib/postgresql/data/%s", d.Name))

	// Write logs to a file instead of inheriting parent's stdout/stderr
	logFile, err := os.OpenFile(
		fmt.Sprintf("/var/log/%s.log", d.Name),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0o644,
	)
	if err != nil {
		log.Fatalf("Failed to open log file: %v", err)
	}

	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	// Run the command in the new namespace
	fullCmd := []string{"ip", "netns", "exec", namespaceName}
	fullCmd = append(fullCmd, cmd.Path)
	fullCmd = append(fullCmd, cmd.Args...)
	cmd = exec.Command(fullCmd[0], fullCmd[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	if err := cmd.Start(); err != nil {
		log.Fatalf("cmd.Start() failed: %s\n", err)
	}

	pid := cmd.Process.Pid
	d.Pid = strconv.Itoa(pid)
	d.cg(pid)
	logFile.Close()
	slog.Info("PostgreSQL process started", "pid", pid)
}

func (d *Database) cg(pid int) {
	cgroupName := d.Name + "_cgroup"

	v2Path := filepath.Join("/sys/fs/cgroup", cgroupName)
	if err := os.MkdirAll(v2Path, 0o755); err == nil {
		// pids.max limits the number of processes in the cgroup
		_ = os.WriteFile(filepath.Join(v2Path, "memory.max"), []byte("200M"), 0o644)
		_ = os.WriteFile(filepath.Join(v2Path, "io.weight"), []byte("100"), 0o644)

		if err := os.WriteFile(
			filepath.Join(v2Path, "cgroup.procs"),
			[]byte(strconv.Itoa(pid)),
			0o644,
		); err != nil {
			log.Fatalf("cgroup v2: failed to write cgroup.procs: %v", err)
		}
		slog.Info("Process added to cgroup v2", "pid", pid, "cgroup", v2Path)
		return
	}
}



func (d *Database) CreateDatabase(body string, ip string) {
	// Populate the database struct from the YAML body
	d.ParseYaml(body)

	switch d.Technology {
	case PostgreSQL:
		slog.Info("Creating PostgreSQL database", "name", d.Name)
		d.execPostgreSQL()
		d.ConnectToBridge(ip)
	default:
		slog.Warn("Unsupported database technology", "technology", d.Technology)
	}
}
