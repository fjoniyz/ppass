package cmd

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"
)

func runList(cmd *cobra.Command, args []string) {
	rdb := newRedisClient()
	defer rdb.Close()

	serviceKeys, _ := rdb.Keys(ctx, "service:*").Result()
	dbKeys, _ := rdb.Keys(ctx, "database:*").Result()
	allKeys := append(serviceKeys, dbKeys...)

	if len(allKeys) == 0 {
		fmt.Println("No active services or databases found.")
		return
	}

	fmt.Printf("%-12s %-20s %-10s %s\n", "TYPE", "NAME", "PID", "IP")
	fmt.Println(strings.Repeat("-", 55))

	for _, key := range allKeys {
		val, err := rdb.Get(ctx, key).Result()
		if err != nil {
			slog.Error("Failed to get value from Redis", "key", key, "error", err)
			continue
		}

		parts := strings.SplitN(key, ":", 2)
		if len(parts) < 2 {
			continue
		}
		resourceType := parts[0]
		resourceName := parts[1]

		var entry Service
		if err := yaml.Unmarshal([]byte(val), &entry); err != nil {
			entry.Pid = val
		}

		fmt.Printf(
			"%-12s %-20s %-10s %s\n",
			resourceType,
			resourceName,
			entry.Pid,
			entry.IP,
		)
	}
}

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all services and databases",
		Run:   runList,
	}
}
