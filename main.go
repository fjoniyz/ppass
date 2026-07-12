package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/redis/go-redis/v9"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"private_paas/cmd"
	"private_paas/parser"
)

type File struct {
	Type string `yaml:"type"`
	Body string `yaml:"body"`
}

type Service struct {
	Pid            string
	ConfigFileName string
}

var ctx = context.Background()

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
		slog.Error("Failed to unmarshal service struct from Redis", "error", err)
		return
	}

	// 1. Kill the process
	pid, _ := strconv.Atoi(service.Pid)
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		slog.Error("Failed to kill process", "error", err)
	} else {
		slog.Info("Killed process", "pid", service.Pid, "name", name)
	}

	// 2. Stop Envoy process
	if err := cmd.StopEnvoy(); err != nil {
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
	_, err = cmd.StartEnvoy()
	if err != nil {
		slog.Error("Failed to restart Envoy process", "error", err)
	} else {
		slog.Info("Restarted Envoy process successfully")
	}

	// 4. Delete from Redis
	if err := rdb.Del(ctx, key).Err(); err != nil {
		slog.Error("Failed to delete process from Redis", "error", err)
	} else {
		slog.Info("Deleted process from Redis", "name", name)
	}
}

// Cleanup function to remove any hanging interfaces
func main() {
	rootCmd := &cobra.Command{
		Use:   "paas",
		Short: "Private PaaS CLI",
	}

	cleanupCmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Cleanup hanging interfaces and processes",
		Run: func(cmd *cobra.Command, args []string) {
			interfaces, err := net.Interfaces()
			if err != nil {
				fmt.Printf("Error fetching interfaces: %v\n", err)
				return
			}

			for _, iface := range interfaces {
				if strings.HasPrefix(iface.Name, "v-") {
					delCmdFormatted := fmt.Sprintf("ip link delete %s", iface.Name)
					delCmd := exec.Command("sh", "-c", delCmdFormatted)
					if err := delCmd.Run(); err != nil {
						slog.Error(
							"Failed to delete interface",
							"interface",
							iface.Name,
							"error",
							err,
						)
					} else {
						slog.Info("Deleted hanging interface", "interface", iface.Name)
					}
				}
			}
		},
	}

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List all services and databases",
		Run: func(cmd *cobra.Command, args []string) {
			rdb := redis.NewClient(&redis.Options{
				Addr:     "localhost:6379",
				Password: "",
				DB:       0,
			})
			defer rdb.Close()

			keys, err := rdb.Keys(ctx, "*").Result()
			if err != nil {
				slog.Error("Failed to fetch keys from Redis", "error", err)
				return
			}
			for _, key := range keys {
				port, err := rdb.Get(ctx, key).Result()
				if err != nil {
					slog.Error("Failed to get value from Redis", "key", key, "error", err)
				}
				resourceType := strings.Split(key, ":")[0]
				resourceName := strings.Split(key, ":")[1]
				fmt.Printf(
					"resource type: %s\t resource name: %s\t port: %s\n",
					resourceType,
					resourceName,
					port,
				)
			}
		},
	}

	createCmd := &cobra.Command{
		Use:   "create [config_file]",
		Short: "Create a service or database from a config file",
		Args:  cobra.ExactArgs(1),
		Run: func(cmdEl *cobra.Command, args []string) {
			rdb := redis.NewClient(&redis.Options{
				Addr:     "localhost:6379",
				Password: "",
				DB:       0,
			})
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

			switch file.Type {
			case "service":
				slog.Info("Creating service with body", "body", file.Body)
				bridge := cmd.CreateBridge()
				s := parser.Service{}
				s.CreateService(file.Body, bridge)
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
				d := parser.Database{}
				d.CreateDatabase(file.Body)
				rdb.Set(ctx, "database:"+d.Name, d.Pid, 0)
			default:
				slog.Warn("Unknown file type", "type", file.Type)
			}
		},
	}

	deleteCmd := &cobra.Command{
		Use:   "delete [type] [name]",
		Short: "Delete a service or database process",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			rdb := redis.NewClient(&redis.Options{
				Addr:     "localhost:6379",
				Password: "",
				DB:       0,
			})
			defer rdb.Close()

			type_ := args[0]
			name := args[1]

			if type_ != "service" && type_ != "database" {
				slog.Error("Invalid type. Must be 'service' or 'database'")
				return
			}

			deleteProcess(rdb, name, type_)
		},
	}

	rootCmd.AddCommand(createCmd, deleteCmd, cleanupCmd, listCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
