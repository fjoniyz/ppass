package cmd

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/vishvananda/netlink"

	"private_paas/utils"
)

type ProcessStruct struct {
	Pid      int
	NsID     string
	VethName string
}

// Pair is a struct representing a virtual ethernet pair
type Pair struct {
	p1 ProcessStruct
	p2 ProcessStruct
}

func RunInNamespace(pid int, args ...string) error {
	fullArgs := append([]string{"-t", strconv.Itoa(pid), "-n"}, args...)
	cmd := exec.Command("nsenter", fullArgs...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("nsenter failed: %v, output: %s", err, string(output))
	}
	return nil
}

func GetNetworkNamespaceForProcess(pid int) (string, error) {
	pidStr := strconv.Itoa(pid)
	nsPath := "/proc/" + pidStr + "/ns/net"
	slog.Info("Getting namespace for process", "pid", pid, "namespace_path", nsPath)
	target, err := os.Readlink(nsPath)
	if err != nil {
		return "", err
	}
	nsID := strings.TrimPrefix(target, "net:[")
	nsID = strings.TrimSuffix(nsID, "]")

	return nsID, nil
}

func MoveLinkToPID(link netlink.Link, pid int) error {
	// Open the namespace file to get a File Descriptor
	f, err := os.Open(fmt.Sprintf("/proc/%d/ns/net", pid))
	if err != nil {
		return fmt.Errorf("failed to open ns for pid %d: %v", pid, err)
	}
	defer f.Close()

	// Move the link into the namespace associated with the FD
	if err := netlink.LinkSetNsFd(link, int(f.Fd())); err != nil {
		return fmt.Errorf("failed to move link %s to pid %d: %v", link.Attrs().Name, pid, err)
	}

	slog.Info("Moved interface into namespace", "interface", link.Attrs().Name, "pid", pid)
	return nil
}

// The process described in this function is as follows:
// 1. Create a veth pair with unique names for the interfaces.
// 2. Move each end of the veth pair into the respective namespaces of the two processes.
// 3. Configure the interfaces inside their namespaces with the provided IP addresses and bring them up.
// 4. Add routes in each namespace to allow communication between the two interfaces.
func createVethPair(pair Pair, ip1, ip2 string) error {
	veth := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{
			Name: pair.p1.VethName,
		},
		PeerName: pair.p2.VethName,
	}
	if err := netlink.LinkAdd(veth); err != nil {
		return fmt.Errorf("failed to create veth pair: %v", err)
	}

	linkFirst, _ := netlink.LinkByName(pair.p1.VethName)
	linkSecond, _ := netlink.LinkByName(pair.p2.VethName)

	// Move links to their respective namespaces FIRST
	if err := MoveLinkToPID(linkFirst, pair.p1.Pid); err != nil {
		return err
	}
	if err := MoveLinkToPID(linkSecond, pair.p2.Pid); err != nil {
		return err
	}

	// Now configure them INSIDE the namespaces using nsenter
	// Configuration for Nginx side (p1)
	if err := RunInNamespace(pair.p1.Pid, "ip", "addr", "add", ip1, "dev", pair.p1.VethName); err != nil {
		return err
	}
	if err := RunInNamespace(pair.p1.Pid, "ip", "link", "set", pair.p1.VethName, "up"); err != nil {
		return err
	}
	// Extract IP without CIDR for routing
	// Look here for the example from Go docs https://pkg.go.dev/net#example-ParseCIDR
	ip2Addr, _, _ := net.ParseCIDR(ip2)
	if err := RunInNamespace(pair.p1.Pid, "ip", "route", "add", ip2Addr.String(), "dev", pair.p1.VethName); err != nil {
		slog.Warn("Failed to add route to service in nginx namespace", "error", err)
	}

	// Configuration for Service side (p2)
	if err := RunInNamespace(pair.p2.Pid, "ip", "addr", "add", ip2, "dev", pair.p2.VethName); err != nil {
		return err
	}
	if err := RunInNamespace(pair.p2.Pid, "ip", "link", "set", pair.p2.VethName, "up"); err != nil {
		return err
	}
	// Extract IP without CIDR for routing
	ip1Addr, _, _ := net.ParseCIDR(ip1)
	if err := RunInNamespace(pair.p2.Pid, "ip", "route", "add", ip1Addr.String(), "dev", pair.p2.VethName); err != nil {
		slog.Warn("Failed to add route to nginx in service namespace", "error", err)
	}

	// Also ensure loopback is up in the service namespace
	if err := RunInNamespace(pair.p2.Pid, "ip", "link", "set", "lo", "up"); err != nil {
		slog.Warn("Failed to set lo up in service namespace", "pid", pair.p2.Pid, "error", err)
	}

	return nil
}

func StopEnvoy() error {
	// Find envoy Pid
	pid, err := utils.FindPidForEnvoy()
	if err != nil {
		return fmt.Errorf("failed to find envoy pid: %v", err)
	}

	if pid == 0 {
		slog.Info("Envoy is not running")
		return nil
	}

	// Kill envoy process using syscall.Kill
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if err != syscall.ESRCH {
			return fmt.Errorf("failed to stop envoy: %v", err)
		}
	}

	// Remove the custom PID file
	_ = os.Remove("/run/envoy-paas.pid")

	slog.Info("Stopped envoy", "pid", pid)
	return nil
}

func moveProcessToEnvoyCgroup(pid int) {
	v2Path := "/sys/fs/cgroup/envoyLb"
	logMsg := ""
	if err := os.MkdirAll(v2Path, 0o755); err == nil {
		_ = os.WriteFile(v2Path+"/pids.max", []byte("100"), 0o644)

		if err := os.WriteFile(v2Path+"/cgroup.procs", []byte(strconv.Itoa(pid)), 0o644); err != nil {
			logMsg = fmt.Sprintf("Failed to write to Envoy cgroup.procs: %v\n", err)
			slog.Warn("Failed to write to Envoy cgroup.procs", "error", err)
		} else {
			logMsg = fmt.Sprintf("Envoy process added to cgroup v2: pid=%d, cgroup=%s\n", pid, v2Path)
			slog.Info("Envoy process added to cgroup v2", "pid", pid, "cgroup", v2Path)
		}

	} else {
		logMsg = fmt.Sprintf("Failed to create cgroup for Envoy: %v\n", err)
		slog.Warn("Failed to create cgroup for Envoy", "error", err)
	}

	// Read self cgroup
	if selfCgroup, err := os.ReadFile("/proc/self/cgroup"); err == nil {
		logMsg += fmt.Sprintf("Self cgroup:\n%s\n", string(selfCgroup))
	}
	// Read Envoy cgroup
	if envoyCgroup, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", pid)); err == nil {
		logMsg += fmt.Sprintf("Envoy cgroup:\n%s\n", string(envoyCgroup))
	}
	// Read envoyLb cgroup.procs
	if procs, err := os.ReadFile(v2Path + "/cgroup.procs"); err == nil {
		logMsg += fmt.Sprintf("envoyLb cgroup.procs:\n%s\n", string(procs))
	}

	_ = os.WriteFile("/var/log/paas-cgroup.log", []byte(logMsg), 0o644)
}

// Start Envoy process
func StartEnvoy() (int, error) {
	// Stop existing PaaS envoy if any
	pid, err := utils.FindPidForEnvoy()
	if err == nil && pid != 0 {
		slog.Info("Stopping existing PaaS envoy process", "pid", pid)
		_ = syscall.Kill(pid, syscall.SIGTERM)
		// Wait a tiny bit for it to terminate
		time.Sleep(100 * time.Millisecond)
	}

	// Ensure the config file exists
	_ = os.MkdirAll("/run", 0o755)
	if _, err := os.Stat("/run/envoy-paas.yaml"); os.IsNotExist(err) {
		bootstrapConfig := "static_resources:\n  listeners: []\n  clusters: []\n"
		_ = os.WriteFile("/run/envoy-paas.yaml", []byte(bootstrapConfig), 0o644)
	}

	// Start envoy with custom config
	cmd := exec.Command("envoy", "-c", "/run/envoy-paas.yaml", "--disable-hot-restart")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:     true,
		Cloneflags: syscall.CLONE_NEWNET,
	}

	logFile, err := os.OpenFile(
		"/var/log/envoy-paas.log",
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0o644,
	)
	if err == nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		// Close the log file handle in the parent after starting the process.
		// The child process maintains its own file descriptor reference.
		defer logFile.Close()
	} else {
		slog.Error("Failed to open Envoy log file", "error", err)
	}

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("failed to start envoy: %v", err)
	}

	pid = cmd.Process.Pid
	slog.Info("Started envoy", "pid", pid)

	// Move Envoy to its own cgroup to prevent systemd from killing it on sudo exit
	moveProcessToEnvoyCgroup(pid)

	// Write PID file
	if err := os.WriteFile("/run/envoy-paas.pid", []byte(strconv.Itoa(pid)), 0o644); err != nil {
		slog.Error("Failed to write Envoy PID file", "error", err)
	}

	// Connect Envoy to the bridge
	bridge := CreateBridge()
	if err := ConnectProcessToBridge(pid, bridge, "10.0.0.1/24", "v-envoy"); err != nil {
		slog.Error("Failed to connect Envoy to bridge", "error", err)
	} else {
		slog.Info("Successfully connected Envoy to bridge", "pid", pid, "ip", "10.0.0.1/24")
	}

	// Wait a bit for envoy to initialize
	time.Sleep(100 * time.Millisecond)

	return pid, nil
}

// Setup bridge that gets used from Nginx. If the bridge already exists, it returns the existing bridge.
func CreateBridge() *netlink.Bridge {
	la := netlink.NewLinkAttrs()
	la.Name = "br0"

	// If bridge already exists, return it
	if link, err := netlink.LinkByName(la.Name); err == nil {
		if bridge, ok := link.(*netlink.Bridge); ok {
			return bridge
		}
		slog.Warn("Link exists but is not a bridge", "name", la.Name)
	}
	bridge := &netlink.Bridge{LinkAttrs: la}

	if err := netlink.LinkAdd(bridge); err != nil {
		slog.Error("Failed to create bridge", "error", err)
	}

	if err := netlink.LinkSetUp(bridge); err != nil {
		slog.Error("Failed to set bridge up", "error", err)
	}

	// Assign IP to bridge so host can talk to it
	addr, _ := netlink.ParseAddr("10.0.0.254/24")
	if err := netlink.AddrAdd(bridge, addr); err != nil &&
		!strings.Contains(err.Error(), "file exists") {
		slog.Warn("Failed to add IP to bridge", "error", err)
	}

	// Fetch it again to get the full object (with Index etc)
	link, _ := netlink.LinkByName(la.Name)
	return link.(*netlink.Bridge)
}

// ConnectProcessToBridge connects a process (by PID) to a bridge using a veth pair.
func ConnectProcessToBridge(
	pid int,
	bridge *netlink.Bridge,
	ip string,
	vethNamePrefix string,
) error {
	// Linux interface names are limited to 15 characters.
	// We truncate the prefix and add a suffix to ensure uniqueness and length compliance.
	safePrefix := vethNamePrefix
	if len(safePrefix) > 9 {
		safePrefix = safePrefix[:9]
	}
	vethInsideName := fmt.Sprintf("%s_%d_n", safePrefix, pid%1000)
	vethOutsideName := fmt.Sprintf("%s_%d_b", safePrefix, pid%1000)

	// Final check to ensure we are within limits
	if len(vethInsideName) > 15 {
		vethInsideName = vethInsideName[:15]
	}
	if len(vethOutsideName) > 15 {
		vethOutsideName = vethOutsideName[:15]
	}

	slog.Info("Creating veth pair", "inside", vethInsideName, "outside", vethOutsideName)

	// Cleanup existing interfaces if they exist
	if link, err := netlink.LinkByName(vethInsideName); err == nil {
		netlink.LinkDel(link)
	}
	if link, err := netlink.LinkByName(vethOutsideName); err == nil {
		netlink.LinkDel(link)
	}

	veth := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{Name: vethInsideName},
		PeerName:  vethOutsideName,
	}

	if err := netlink.LinkAdd(veth); err != nil {
		return fmt.Errorf("failed to create veth pair : %v", err)
	}

	vethInside, err := netlink.LinkByName(vethInsideName)
	if err != nil {
		return fmt.Errorf("failed to find veth inside: %v", err)
	}
	vethOutside, err := netlink.LinkByName(vethOutsideName)
	if err != nil {
		return fmt.Errorf("failed to find veth outside: %v", err)
	}

	// Attach outside end to bridge
	if err := netlink.LinkSetMaster(vethOutside, bridge); err != nil {
		return fmt.Errorf("failed to attach veth to bridge: %v", err)
	}

	// Put the end up
	if err := netlink.LinkSetUp(vethOutside); err != nil {
		return fmt.Errorf("failed to set veth outside up: %v", err)
	}

	// Move inside end to namespace
	if err := MoveLinkToPID(vethInside, pid); err != nil {
		return fmt.Errorf("failed to move veth inside to namespace: %v", err)
	}

	// Configure inside end to run in the namespace with the given IP and bring it up
	if err := RunInNamespace(pid, "ip", "addr", "add", ip, "dev", vethInsideName); err != nil {
		return fmt.Errorf("failed to add IP inside namespace: %v", err)
	}

	if err := RunInNamespace(pid, "ip", "link", "set", vethInsideName, "up"); err != nil {
		return fmt.Errorf("failed to set veth inside up: %v", err)
	}

	// Also ensure loopback is up
	if err := RunInNamespace(pid, "ip", "link", "set", "lo", "up"); err != nil {
		slog.Warn("Failed to set lo up in namespace", "pid", pid, "error", err)
	}

	return nil
}
