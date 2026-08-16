package cmd

import (
	"context"

	"github.com/redis/go-redis/v9"
	"github.com/spf13/cobra"
)

type File struct {
	Type string `yaml:"type"`
	Body string `yaml:"body"`
}

type Service struct {
	Pid            string `yaml:"pid"`
	ConfigFileName string `yaml:"configfilename"`
	IP             string `yaml:"ip"`
}

var (
	ctx     = context.Background()
	rootCmd = &cobra.Command{
		Use:   "paas",
		Short: "Private PaaS CLI",
	}
)

func newRedisClient() *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "",
		DB:       0,
	})
}

func init() {
	rootCmd.AddCommand(
		newCreateCmd(),
		newDeleteCmd(),
		newCleanupCmd(),
		newListCmd(),
	)
}

// Execute executes the root command.
func Execute() error {
	return rootCmd.Execute()
}

// GetRootCmd returns the root command instance (useful for testing).
func GetRootCmd() *cobra.Command {
	return rootCmd
}
