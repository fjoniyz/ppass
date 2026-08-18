package parser

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"private_paas/network"
)

func findPostgresBin() string {
	if path, err := exec.LookPath("postgres"); err == nil {
		return path
	}
	candidates := []string{
		"/usr/lib/postgresql/14/bin/postgres",
		"/usr/lib/postgresql/12/bin/postgres",
		"/usr/lib/postgresql/16/bin/postgres",
		"/usr/lib/postgresql/15/bin/postgres",
		"/usr/lib/postgresql/13/bin/postgres",
		"/usr/local/bin/postgres",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return "postgres"
}

func findInitdbBin() string {
	if path, err := exec.LookPath("initdb"); err == nil {
		return path
	}
	candidates := []string{
		"/usr/lib/postgresql/14/bin/initdb",
		"/usr/lib/postgresql/12/bin/initdb",
		"/usr/lib/postgresql/16/bin/initdb",
		"/usr/lib/postgresql/15/bin/initdb",
		"/usr/lib/postgresql/13/bin/initdb",
		"/usr/local/bin/initdb",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return "initdb"
}

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
	var postgresUid, postgresGid uint32 = 0, 0
	if u, err := user.Lookup("postgres"); err == nil {
		if uid, err := strconv.ParseUint(u.Uid, 10, 32); err == nil {
			postgresUid = uint32(uid)
		}
		if gid, err := strconv.ParseUint(u.Gid, 10, 32); err == nil {
			postgresGid = uint32(gid)
		}
	}

	dataDir := fmt.Sprintf("/var/lib/postgresql/data/%s", d.Name)
	_ = os.MkdirAll("/var/lib/postgresql/data", 0o755)

	// 1. Initialize data directory with initdb if it doesn't exist yet
	if _, err := os.Stat(filepath.Join(dataDir, "PG_VERSION")); os.IsNotExist(err) {
		_ = os.MkdirAll(dataDir, 0o700)
		if postgresUid != 0 {
			_ = os.Chown(dataDir, int(postgresUid), int(postgresGid))
		}
		initdbBin := findInitdbBin()
		initCmd := exec.Command(initdbBin, "-D", dataDir, "-U", "postgres", "-A", "trust")
		if postgresUid != 0 {
			initCmd.SysProcAttr = &syscall.SysProcAttr{
				Credential: &syscall.Credential{
					Uid: postgresUid,
					Gid: postgresGid,
				},
			}
		}
		if out, err := initCmd.CombinedOutput(); err != nil {
			slog.Error("initdb failed", "error", err, "output", string(out))
		} else {
			slog.Info("Successfully initialized database cluster with initdb", "dataDir", dataDir)
		}
	}

	// Ensure pg_hba.conf allows connections from the bridge subnet
	pgHbaPath := filepath.Join(dataDir, "pg_hba.conf")
	if data, err := os.ReadFile(pgHbaPath); err == nil {
		if !strings.Contains(string(data), "0.0.0.0/0") {
			if f, err := os.OpenFile(pgHbaPath, os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
				_, _ = f.WriteString("\nhost all all 0.0.0.0/0 trust\n")
				f.Close()
				slog.Info("Appended remote connection rule to pg_hba.conf", "path", pgHbaPath)
			}
		}
	}

	if postgresUid != 0 {
		_ = os.Chown(dataDir, int(postgresUid), int(postgresGid))
	}

	// 2. Start postgres server daemon
	postgresBin := findPostgresBin()
	cmd := exec.Command(
		postgresBin,
		"-D", dataDir,
		"-c", "listen_addresses=*",
		"-c", "unix_socket_directories=/tmp",
	)

	// Write logs to /var/log/<name>.log
	logFilePath := fmt.Sprintf("/var/log/%s.log", d.Name)
	logFile, err := os.OpenFile(
		logFilePath,
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0o666,
	)
	if err != nil {
		log.Fatalf("Failed to open log file: %v", err)
	}
	if postgresUid != 0 {
		_ = os.Chown(logFilePath, int(postgresUid), int(postgresGid))
	}

	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	// Create dedicated network namespace and drop permissions to postgres user
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:     true,
		Cloneflags: syscall.CLONE_NEWNET,
	}
	if postgresUid != 0 {
		cmd.SysProcAttr.Credential = &syscall.Credential{
			Uid: postgresUid,
			Gid: postgresGid,
		}
	}

	if err := cmd.Start(); err != nil {
		log.Fatalf("cmd.Start() failed: %s\n", err)
	}

	pid := cmd.Process.Pid
	d.Pid = strconv.Itoa(pid)

	// Wait for the process network namespace to be available in /proc
	maxRetries := 20
	for i := 0; i < maxRetries; i++ {
		if _, err := os.Stat(fmt.Sprintf("/proc/%d/ns/net", pid)); err == nil {
			break
		}
		if i == maxRetries-1 {
			log.Fatalf("Process %d network namespace not available after timeout", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}

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
