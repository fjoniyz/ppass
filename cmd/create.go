package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"private_paas/network"
	"private_paas/parser"
)

func runCreate(cmd *cobra.Command, args []string) {
	rdb := newRedisClient()
	defer rdb.Close()

	filepath := args[0]
	f, err := os.ReadFile(filepath)
	if err != nil {
		slog.Error("Failed to read config file", "error", err)
		return
	}

	var file File
	if err := yaml.Unmarshal(f, &file); err != nil {
		slog.Error("Failed to unmarshal config file", "error", err)
		return
	}

	// Using goipam as IP allocator manager
	ipamStruct := network.GetIp()

	switch file.Type {
	case "service":
		slog.Info("Creating service with body", "body", file.Body)
		slog.Info("Allocated IP for service", "ip", ipamStruct.Ip)
		bridge := network.CreateBridge()
		s := parser.Service{}
		s.CreateService(file.Body, bridge, ipamStruct.Ip)
		pidString := strconv.Itoa(s.Pid)

		envoyConfigPath := ""
		if s.Lb {
			envoyConfigPath = fmt.Sprintf(
				"/etc/envoy/conf.d/%s.yaml",
				s.LbConfig.UpstreamName,
			)
		}

		redisServiceEntry := Service{
			Pid:            pidString,
			ConfigFileName: envoyConfigPath,
			IP:             ipamStruct.Ip,
		}

		// Marshal the struct to a string for storage in Redis
		redisServiceEntryStr, err := yaml.Marshal(redisServiceEntry)
		if err != nil {
			slog.Error("Failed to marshal service struct for Redis", "error", err)
			return
		}

		// Now we can store the string. You need to also unmarshal it back to the struct when retrieving from Redis.
		rdb.Set(ctx, "service:"+s.Name, redisServiceEntryStr, 0)

	case "db":
		slog.Info("Creating database with body", "body", file.Body)
		slog.Info("Allocated IP for database", "ip", ipamStruct.Ip)
		d := parser.Database{}
		d.CreateDatabase(file.Body, ipamStruct.Ip)
		dbEntry := Service{
			Pid: d.Pid,
			IP:  ipamStruct.Ip,
		}
		dbEntryStr, _ := yaml.Marshal(dbEntry)
		rdb.Set(ctx, "database:"+d.Name, dbEntryStr, 0)
	default:
		slog.Warn("Unknown file type", "type", file.Type)
	}
}

func newCreateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create [config_file]",
		Short: "Create a service or database from a config file",
		Args:  cobra.ExactArgs(1),
		Run:   runCreate,
	}
}
