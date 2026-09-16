package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/mikeoertli/kube_context_manager/internal/kcm"
)

var version = "0.1.0-dev"

func main() {
	if err := kcm.NewCommand(version).Execute(); err != nil {
		if err.Error() != "" {
			fmt.Fprintln(os.Stderr, "kcm:", err)
		}
		var status *kcm.ExitError
		if errors.As(err, &status) {
			os.Exit(status.Code)
		}
		os.Exit(1)
	}
}
