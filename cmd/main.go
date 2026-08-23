package main

import (
	"fmt"
	"os"

	"github.com/gsdevme/trading212-mqtt/internal/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
