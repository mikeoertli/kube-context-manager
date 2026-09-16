package main

import (
	"errors"
	"fmt"
	"os"

	project "github.com/mikeoertli/kube-context-manager"
	"github.com/mikeoertli/kube-context-manager/internal/kcm"
)

func main() {
	if err := kcm.NewCommand(project.Version()).Execute(); err != nil {
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
