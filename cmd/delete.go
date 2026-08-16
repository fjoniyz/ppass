package cmd

import (
	"log/slog"
	"os"
	"strconv"
	"syscall"

	"github.com/redis/go-redis/v9"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"private_paas/network"
)

func deleteProcess(rdb *redis.Client, name string, type_ string) {
	key := type_ + ":" + name
	serviceStruct, err := rdb.Get(ctx, key).Result()
	if err != nil {
		slog.Error("Failed to get process from Redis", "error", err)
		return
	}

	// ServiceStruct is stored as a YAML string in Redis, so we need to unmarshal it back to the struct
	var service Service
	if err := yaml.Unmarshal([]byte(serviceStruct), &service); err != nil {
		// Fallback if stored as plain pid string
		service.Pid = serviceStruct
	}

	// Release IP address back to IPAM pool
	if service.IP != "" {
		if err := network.ReleaseIp(service.IP); err != nil {
			slog.Error("Failed to release IP for process", "ip", service.IP, "error", err)
		}
	}

	// 1. Kill the process
	pid, _ := strconv.Atoi(service.Pid)
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		slog.Error("Failed to kill process", "error", err)
	} else {
		slog.Info("Killed process", "pid", service.Pid, "name", name)
	}

	// 2. Stop Envoy process
	if err := network.StopEnvoy(); err != nil {
		slog.Error("Failed to stop Envoy process", "error", err)
	}
	// 3. Delete the config file from Envoy
	if service.ConfigFileName != "" {
		if err := os.Remove(service.ConfigFileName); err != nil {
			slog.Error("Failed to delete Envoy config file", "error", err)
		} else {
			slog.Info("Deleted Envoy config file", "file", service.ConfigFileName)
		}
	}
	// 4. Restart Envoy to apply the changes
	_, err = network.StartEnvoy()
	if err != nil {
		slog.Error("Failed to restart Envoy process", "error", err)
	} else {
		slog.Info("Restarted Envoy process successfully")
	}

	// 5. Delete from Redis
	if err := rdb.Del(ctx, key).Err(); err != nil {
		slog.Error("Failed to delete process from Redis", "error", err)
	} else {
		slog.Info("Deleted process from Redis", "name", name)
	}
}

func runDelete(cmd *cobra.Command, args []string) {
	rdb := newRedisClient()
	defer rdb.Close()

	type_ := args[0]
	name := args[1]

	if type_ != "service" && type_ != "database" {
		slog.Error("Invalid type. Must be 'service' or 'database'")
		return
	}

	deleteProcess(rdb, name, type_)
}

func newDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete [type] [name]",
		Short: "Delete a service or database process",
		Args:  cobra.ExactArgs(2),
		Run:   runDelete,
	}
}
