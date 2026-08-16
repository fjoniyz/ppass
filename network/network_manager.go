package network

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	goipam "github.com/metal-stack/go-ipam"
	"github.com/redis/go-redis/v9"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"

	"private_paas/utils"
)

type IpamStruct struct {
	Ip   string
	Ipam *goipam.Ipamer
}

type ProcessStruct struct {
	Pid      int
	NsID     string
	VethName string
	Ipam     IpamStruct
}

// Pair is a struct representing a virtual ethernet pair
type Pair struct {
	p1 ProcessStruct
	p2 ProcessStruct
}

func configureInterfaceInNamespace(
	pid int,
	vethName string,
	ipStr string,
	peerIP net.IP,
	enableLo bool,
) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	origns, err := netns.Get()
	if err != nil {
		return fmt.Errorf("failed to get current netns: %w", err)
	}
	defer origns.Close()

	targetns, err := netns.GetFromPid(pid)
	if err != nil {
		return fmt.Errorf("failed to get netns for pid %d: %w", pid, err)
	}
	defer targetns.Close()

	if err := netns.Set(targetns); err != nil {
		return fmt.Errorf("failed to enter netns for pid %d: %w", pid, err)
	}
	defer func() {
		if err := netns.Set(origns); err != nil {
			slog.Error("Failed to restore original netns", "error", err)
		}
	}()
	link, err := netlink.LinkByName(vethName)
	if err != nil {
		return fmt.Errorf("failed to find link %s inside namespace: %w", vethName, err)
	}

	slog.Info("Configuring interface in namespace", "pid", pid, "vethName", vethName, "ip", ipStr)
	cidr := ipStr
	if !strings.Contains(cidr, "/") {
		cidr += "/24"
	}
	addr, err := netlink.ParseAddr(cidr)
	if err != nil {
		return fmt.Errorf("failed to parse IP address %s: %w", ipStr, err)
	}
	if err := netlink.AddrAdd(link, addr); err != nil {
		return fmt.Errorf("failed to add IP %s to link %s: %w", ipStr, vethName, err)
	}

	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("failed to set link %s up: %w", vethName, err)
	}

	if peerIP != nil {
		route := &netlink.Route{
			LinkIndex: link.Attrs().Index,
			Dst: &net.IPNet{
				IP:   peerIP,
				Mask: net.CIDRMask(32, 32),
			},
		}
		if err := netlink.RouteAdd(route); err != nil {
			slog.Warn(
				"Failed to add route to peer in namespace",
				"pid",
				pid,
				"peer",
				peerIP,
				"error",
				err,
			)
		}
	}

	if enableLo {
		if lo, err := netlink.LinkByName("lo"); err == nil {
			if err := netlink.LinkSetUp(lo); err != nil {
				slog.Warn("Failed to set lo up in namespace", "pid", pid, "error", err)
			}
		}
	}

	if !strings.HasPrefix(vethName, "v-envoy") {
		pidStr := strconv.Itoa(pid)
		_ = exec.Command("nsenter", "-t", pidStr, "-n", "iptables", "-A", "INPUT", "-i", "lo", "-j", "ACCEPT").Run()
		_ = exec.Command("nsenter", "-t", pidStr, "-n", "iptables", "-A", "INPUT", "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT").Run()
		_ = exec.Command("nsenter", "-t", pidStr, "-n", "iptables", "-A", "INPUT", "-s", "10.0.0.1", "-j", "ACCEPT").Run()
		_ = exec.Command("nsenter", "-t", pidStr, "-n", "iptables", "-A", "INPUT", "-j", "DROP").Run()
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

	// Now configure them INSIDE the namespaces using netns/netlink helper
	ip2Addr, _, _ := net.ParseCIDR(ip2)
	ip1Addr, _, _ := net.ParseCIDR(ip1)

	// Configuration for Envoy side (p1)
	if err := configureInterfaceInNamespace(pair.p1.Pid, pair.p1.VethName, ip1, ip2Addr, false); err != nil {
		return err
	}

	// Configuration for Service side (p2)
	if err := configureInterfaceInNamespace(pair.p2.Pid, pair.p2.VethName, ip2, ip1Addr, true); err != nil {
		return err
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

var (
	memStorage     goipam.Storage
	memStorageOnce sync.Once
)

func getFallbackStorage(ctx context.Context) goipam.Storage {
	memStorageOnce.Do(func() {
		memStorage = goipam.NewMemory(ctx)
	})
	return memStorage
}

func GetIpam() (goipam.Ipamer, error) {
	ctx := context.Background()
	var storage goipam.Storage

	// Probe Redis with a quick timeout
	rdb := redis.NewClient(&redis.Options{
		Addr:        "localhost:6379",
		DialTimeout: 200 * time.Millisecond,
	})
	pingCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	err := rdb.Ping(pingCtx).Err()
	cancel()
	_ = rdb.Close()

	if err == nil {
		redisStorage, rErr := goipam.NewRedis(ctx, "localhost", "6379")
		if rErr == nil {
			storage = redisStorage
		}
	}

	if storage == nil {
		storage = getFallbackStorage(ctx)
	}

	ipam := goipam.NewWithStorage(storage)

	prefix, err := ipam.PrefixFrom(ctx, "10.0.0.0/24")
	if err != nil || prefix == nil {
		_, err = ipam.NewPrefix(ctx, "10.0.0.0/24")
		if err != nil && !strings.Contains(err.Error(), "already") {
			return nil, fmt.Errorf("failed to create IPAM prefix: %w", err)
		}
		// Reserve 10.0.0.254 for host bridge br0
		_, _ = ipam.AcquireSpecificIP(ctx, "10.0.0.0/24", "10.0.0.254")
	}

	return ipam, nil
}

func ReleaseIp(ipStr string) error {
	ipam, err := GetIpam()
	if err != nil {
		return err
	}
	ctx := context.Background()
	cleanIP := strings.Split(ipStr, "/")[0]
	cleanIP = strings.TrimSpace(cleanIP)
	if cleanIP == "" {
		return nil
	}
	err = ipam.ReleaseIPFromPrefix(ctx, "10.0.0.0/24", cleanIP)
	if err != nil {
		slog.Error("Failed to release IP", "ip", cleanIP, "error", err)
		return err
	}
	slog.Info("Successfully released IP", "ip", cleanIP)
	return nil
}

func GetOrAcquireEnvoyIp() (string, error) {
	ipam, err := GetIpam()
	if err != nil {
		return "", err
	}
	ctx := context.Background()

	// Specifically allocate 10.0.0.1 for Envoy gateway if available
	ip, err := ipam.AcquireSpecificIP(ctx, "10.0.0.0/24", "10.0.0.1")
	if err == nil && ip != nil {
		return ip.IP.String(), nil
	}

	// 10.0.0.1 was already allocated (e.g. from previous run), reuse it for Envoy
	return "10.0.0.1", nil
}

func GetIp() IpamStruct {
	ipam, err := GetIpam()
	if err != nil {
		slog.Error("Failed to get IPAM instance", "error", err)
		return IpamStruct{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ip, err := ipam.AcquireIP(ctx, "10.0.0.0/24")
	if err != nil || ip == nil {
		slog.Error("Failed to acquire IP", "error", err)
		return IpamStruct{}
	}

	ipamStruct := IpamStruct{
		Ip:   ip.IP.String(),
		Ipam: &ipam,
	}

	return ipamStruct
}

// Start Envoy process
// It gets the IP address for the process
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
	envoyBin := "envoy"
	if path, err := exec.LookPath("envoy"); err == nil {
		envoyBin = path
	} else if _, err := os.Stat("/home/linuxbrew/.linuxbrew/bin/envoy"); err == nil {
		envoyBin = "/home/linuxbrew/.linuxbrew/bin/envoy"
	} else if _, err := os.Stat("/usr/local/bin/envoy"); err == nil {
		envoyBin = "/usr/local/bin/envoy"
	}

	cmd := exec.Command(envoyBin, "-c", "/run/envoy-paas.yaml", "--disable-hot-restart")
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

	ip, err := GetOrAcquireEnvoyIp()
	if err != nil {
		slog.Error("Failed to acquire IP for Envoy", "error", err)
		return 0, err
	}
	slog.Info("Acquired IP for Envoy", "ip", ip)

	// Connect Envoy to the bridge
	bridge := CreateBridge()
	if err := ConnectProcessToBridge(pid, bridge, ip, "v-envoy"); err != nil {
		slog.Error("Failed to connect Envoy to bridge", "error", err)
	} else {
		slog.Info("Successfully connected Envoy to bridge", "pid", pid, "ip", ip)
	}

	// Wait a bit for envoy to initialize
	time.Sleep(100 * time.Millisecond)

	return pid, nil
}

// Setup bridge that gets used from Envoy. If the bridge already exists, it returns the existing bridge.
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
	if err := configureInterfaceInNamespace(pid, vethInsideName, ip, nil, true); err != nil {
		return fmt.Errorf("failed to configure veth inside in namespace: %w", err)
	}

	return nil
}
