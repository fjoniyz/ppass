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
	"text/template"

	"github.com/vishvananda/netlink"
	"gopkg.in/yaml.v2"

	"private_paas/cmd"
	"private_paas/utils"
)

// This is the struct for the configuration of the Envoy load balancer itself.
type EnvoyConfig struct {
	Ports       []int
	DomainNames []string
}

func (c *EnvoyConfig) cg(pid int) {
	const cgroupName = "envoyLb"

	v2Path := filepath.Join("/sys/fs/cgroup", cgroupName)
	if err := os.MkdirAll(v2Path, 0o755); err == nil {
		_ = os.WriteFile(filepath.Join(v2Path, "pids.max"), []byte("100"), 0o644)

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

// We should first check if the Envoy process is already running. If it is, we do nothing and just return the PID. Else we create it.
func CreateEnvoy(bridge *netlink.Bridge) int {
	pid, _ := utils.FindPidForEnvoy()
	if pid != 0 {
		fmt.Printf("Envoy is already running with PID: %d\n", pid)
		return pid
	}

	cmd := exec.Command("envoy", "-c", "/run/envoy-paas.yaml")
	if err := cmd.Start(); err != nil {
		fmt.Printf("Failed to start envoy: %v\n", err)
		return 0
	}

	fmt.Printf("Started envoy with PID: %d\n", cmd.Process.Pid)
	return cmd.Process.Pid
}

type EnvoyServer struct {
	Address string
	Port    int
}

type EnvoyService struct {
	UpstreamName string
	ListenPort   int
	Domain       string
	Servers      []EnvoyServer
}

func UpdateLbConfig(config ServiceEnvoyConfig) {
	slog.Info("Updating Envoy LB config", "upstream", config.UpstreamName, "listen_port", config.ListenPort)

	// 1. Ensure directory exists
	envoyConfDir := "/etc/envoy/conf.d"
	if err := os.MkdirAll(envoyConfDir, 0755); err != nil {
		panic(fmt.Errorf("failed to create envoy conf.d directory: %v", err))
	}

	// 2. Write the service config to its own file
	serviceConfPath := filepath.Join(envoyConfDir, fmt.Sprintf("%s.yaml", config.UpstreamName))
	configData, err := yaml.Marshal(config)
	if err != nil {
		panic(fmt.Errorf("failed to marshal service config: %v", err))
	}
	if err := os.WriteFile(serviceConfPath, configData, 0644); err != nil {
		panic(fmt.Errorf("failed to write service config file: %v", err))
	}

	// 3. Read all files in /etc/envoy/conf.d/*.yaml and parse them
	files, err := filepath.Glob(filepath.Join(envoyConfDir, "*.yaml"))
	if err != nil {
		panic(err)
	}

	var envoyServices []EnvoyService
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			slog.Warn("Failed to read config file", "file", file, "error", err)
			continue
		}
		var srvConfig ServiceEnvoyConfig
		if err := yaml.Unmarshal(data, &srvConfig); err != nil {
			slog.Warn("Failed to unmarshal config file", "file", file, "error", err)
			continue
		}

		// Convert ServiceEnvoyConfig to EnvoyService
		es := EnvoyService{
			UpstreamName: srvConfig.UpstreamName,
			ListenPort:   srvConfig.ListenPort,
			Domain:       srvConfig.Domain,
		}
		for _, srv := range srvConfig.Servers {
			parts := strings.Split(srv, ":")
			port := 80
			if len(parts) > 1 {
				if p, err := strconv.Atoi(parts[1]); err == nil {
					port = p
				}
			}
			es.Servers = append(es.Servers, EnvoyServer{
				Address: parts[0],
				Port:    port,
			})
		}
		envoyServices = append(envoyServices, es)
	}

	// 4. Render Envoy config template
	pwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	tmplFilePath := filepath.Join(pwd, "parser/templates/envoy.tmpl")
	t, err := template.ParseFiles(tmplFilePath)
	if err != nil {
		panic(err)
	}

	f, err := os.Create("/run/envoy-paas.yaml")
	if err != nil {
		panic(err)
	}
	defer f.Close()

	if err := t.Execute(f, envoyServices); err != nil {
		panic(err)
	}

	slog.Info("Successfully generated Envoy config", "path", "/run/envoy-paas.yaml")

	// 5. Restart Envoy to apply changes
	if _, err := cmd.StartEnvoy(); err != nil {
		slog.Error("Failed to restart Envoy", "error", err)
	} else {
		slog.Info("Successfully restarted Envoy")
	}
}
