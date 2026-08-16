package cmd

import (
	"fmt"
	"log/slog"
	"net"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

// Cleanup function to remove any hanging interfaces
func runCleanup(cmd *cobra.Command, args []string) {
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
}

func newCleanupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cleanup",
		Short: "Cleanup hanging interfaces and processes",
		Run:   runCleanup,
	}
}
