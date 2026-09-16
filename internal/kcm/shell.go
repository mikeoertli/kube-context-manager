package kcm

import (
	_ "embed"
	"fmt"
	"io"
	"os"
)

//go:embed shell/common.sh
var commonShell string

//go:embed shell/zsh.sh
var zshShell string

//go:embed shell/bash.sh
var bashShell string

func shellInit(w io.Writer, shell, settings string) error {
	if shell != "zsh" && shell != "bash" {
		return fmt.Errorf("supported shells: zsh, bash (4.4+)")
	}
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "# kcm shell integration; source after other shell/keybinding plugins.")
	emit(w, "KCM_SETTINGS", settings)
	fmt.Fprintf(w, "_KCM_BIN=%s\n", quote(bin))
	fmt.Fprintln(w, commonShell)
	if shell == "zsh" {
		fmt.Fprintln(w, zshShell)
	} else {
		fmt.Fprintln(w, bashShell)
	}
	return nil
}
