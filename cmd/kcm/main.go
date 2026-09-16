package main

import (
	"fmt"
	"os"

	"github.com/mikeoertli/kube_context_manager/internal/kcm"
)

var version = "0.1.0-dev"

func main() {
	if err := kcm.NewCommand(version).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "kcm:", err)
		os.Exit(1)
	}
}
