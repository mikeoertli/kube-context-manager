package kcm

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func readClipboard() ([]byte, error) {
	commands := [][]string{{"xclip", "-selection", "clipboard", "-out"}, {"xsel", "--clipboard", "--output"}}
	if runtime.GOOS == "darwin" {
		commands = [][]string{{"pbpaste"}}
	} else if os.Getenv("WAYLAND_DISPLAY") != "" {
		commands = append([][]string{{"wl-paste", "--no-newline"}}, commands...)
	} else {
		commands = append(commands, []string{"wl-paste", "--no-newline"})
	}
	for _, args := range commands {
		path, err := exec.LookPath(args[0])
		if err != nil {
			continue
		}
		content, err := exec.Command(path, args[1:]...).Output()
		if err != nil {
			return nil, fmt.Errorf("could not read clipboard using %s: %w", args[0], err)
		}
		return content, nil
	}
	return nil, fmt.Errorf("clipboard reader unavailable; macOS requires pbpaste, Linux requires wl-paste, xclip, or xsel")
}

func writeClipboard(content []byte) error {
	commands := [][]string{{"xclip", "-selection", "clipboard", "-in"}, {"xsel", "--clipboard", "--input"}}
	if runtime.GOOS == "darwin" {
		commands = [][]string{{"pbcopy"}}
	} else if os.Getenv("WAYLAND_DISPLAY") != "" {
		commands = append([][]string{{"wl-copy"}}, commands...)
	} else {
		commands = append(commands, []string{"wl-copy"})
	}
	for _, args := range commands {
		path, err := exec.LookPath(args[0])
		if err != nil {
			continue
		}
		cmd := exec.Command(path, args[1:]...)
		cmd.Stdin = bytes.NewReader(content)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("could not write clipboard using %s: %w", args[0], err)
		}
		return nil
	}
	return fmt.Errorf("clipboard writer unavailable; macOS requires pbcopy, Linux requires wl-copy, xclip, or xsel")
}

func clipboardSource() (string, func(), error) {
	content, err := readClipboard()
	if err != nil {
		return "", nil, err
	}
	if len(bytes.TrimSpace(content)) == 0 {
		return "", nil, fmt.Errorf("clipboard is empty; copy kubeconfig text first")
	}
	dir, err := os.MkdirTemp("", "kcm-clipboard-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(dir) }
	path := filepath.Join(dir, "clipboard.kubeconfig")
	if err := os.WriteFile(path, content, 0600); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}
