package main

import (
	"fmt"
	"os"

	"private_paas/cmd"
	"private_paas/parser"
)

func main() {
	if os.Getenv("_PAAS_CONTAINER_INIT") == "1" {
		parser.RunContainerInit(os.Getenv("_PAAS_SERVICE_PATH"))
		return
	}

	if err := cmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
