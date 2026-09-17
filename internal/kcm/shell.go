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
	s, err := loadSettings(settings)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "# kcm shell integration; source after other shell/keybinding plugins.")
	emit(w, "KCM_SETTINGS", settings)
	fmt.Fprintf(w, "_KCM_BIN=%s\n", quote(bin))
	s.emitPromptMetadata(w, "local")
	fmt.Fprintln(w, commonShell)
	if shell == "zsh" {
		fmt.Fprintln(w, zshShell)
	} else {
		fmt.Fprintln(w, bashShell)
	}
	return nil
}

// Refresh symbols during explicit KCM actions, never while rendering a prompt.
func (s *Settings) emitPromptMetadata(w io.Writer, profile string) {
	fmt.Fprintf(w, "_KCM_PROFILE_EMOJI=%s\n_KCM_LOCAL_EMOJI=%s\n",
		quote(s.Profiles[profile].Emoji), quote(s.Profiles["local"].Emoji))
}
