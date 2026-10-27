package kcm

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const selectionPrefix = "kcm paste --selection "
const invalidSelection = "clipboard does not contain a kcm profile + context; run kcm copy in the source shell"
const maxSelectionSize = 64 * 1024

type copiedSelection struct {
	Version   int    `json:"version"`
	Settings  string `json:"settings"`
	Profile   string `json:"profile"`
	Context   string `json:"context"`
	File      string `json:"file"`
	Server    string `json:"server"`
	ExpiresAt string `json:"expires_at"`
}

func encodeSelection(selection copiedSelection) string {
	data, _ := json.Marshal(selection)
	digest := sha256.Sum256(data)
	return "kcm1." + base64.RawURLEncoding.EncodeToString(data) + "." + hex.EncodeToString(digest[:])
}

func decodeSelection(token string) (copiedSelection, error) {
	invalid := func() (copiedSelection, error) { return copiedSelection{}, fmt.Errorf("%s", invalidSelection) }
	if len(token) > maxSelectionSize {
		return invalid()
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "kcm1" {
		return invalid()
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return invalid()
	}
	digest := sha256.Sum256(data)
	if parts[2] != hex.EncodeToString(digest[:]) {
		return invalid()
	}
	var selection copiedSelection
	if err := json.Unmarshal(data, &selection); err != nil {
		return invalid()
	}
	canonical, _ := json.Marshal(selection)
	// Reject unknown/duplicate fields and alternate representations, not only
	// bit flips. Copy always emits the same versioned JSON representation.
	if !bytes.Equal(canonical, data) || selection.Version != 1 {
		return invalid()
	}
	for _, value := range []string{selection.Settings, selection.Profile, selection.Context, selection.File, selection.Server} {
		if value == "" || validName(value) != nil {
			return invalid()
		}
	}
	if !filepath.IsAbs(selection.Settings) || !filepath.IsAbs(selection.File) {
		return invalid()
	}
	if selection.ExpiresAt != "" {
		deadline, err := strconv.ParseInt(selection.ExpiresAt, 10, 64)
		if err != nil || deadline <= 0 || strconv.FormatInt(deadline, 10) != selection.ExpiresAt {
			return invalid()
		}
	}
	return selection, nil
}

func selectionDeadline(deadline string, now time.Time) error {
	if deadline == "" {
		return nil
	}
	seconds, err := strconv.ParseInt(deadline, 10, 64)
	if err != nil || seconds <= 0 {
		return fmt.Errorf("selected context has an invalid timeout; select it again before copying")
	}
	if seconds <= now.Unix() {
		return fmt.Errorf("copied context has expired; renew or select it again in the source shell and run kcm copy")
	}
	return nil
}

func copySelectionCommand(load func() (*Settings, error), shell bool) *cobra.Command {
	return &cobra.Command{
		Use: "copy", Aliases: []string{"cp"}, Short: "Copy this shell's profile and context for another terminal",
		Long:    "Copy a pasteable KCM command to the clipboard. Includes the selected profile, exact context/file, settings path, and existing timeout deadline. Does not copy kubeconfig contents, credentials, or history. The receiving shell must have access to the same paths. In another initialized shell, run kcm paste, or use Edit > Paste / Cmd+V and press Enter. Expired selections cannot be copied.",
		Example: "  kcm copy\n  kcm cp\n  # In the other terminal:\n  kcm paste",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			c, err := s.active()
			if err != nil {
				return err
			}
			deadline := os.Getenv("KCM_EXPIRES_AT")
			if err := selectionDeadline(deadline, time.Now()); err != nil {
				return err
			}
			selection := copiedSelection{Version: 1, Settings: s.Path, Profile: currentProfile(), Context: c.Name, File: c.File, Server: c.Server, ExpiresAt: deadline}
			token := encodeSelection(selection)
			if _, err := decodeSelection(token); err != nil {
				return fmt.Errorf("cannot copy selection: %w", err)
			}
			if err := writeClipboard([]byte(selectionPrefix + token)); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if shell {
				out = cmd.ErrOrStderr()
			} // Older integrations may route copy through __shell.
			fmt.Fprintf(out, "Copied profile %s · context %s. Use kcm paste or Cmd+V and Enter in the other terminal.\n", selection.Profile, selection.Context)
			return nil
		},
	}
}

func pasteSelectionCommand(shell bool) *cobra.Command {
	var token string
	cmd := &cobra.Command{
		Use: "paste", Short: "Apply a copied KCM profile and context to this shell",
		Long:    "Validate and apply the profile/context copied by kcm copy. Reads the clipboard unless invoked by the generated pasteable command. Restores the source settings path, exact kubeconfig/context, and existing timeout deadline without extending it. Destination history remains shell-local. Damaged/unrelated clipboard contents, expired selections, missing configs, and changed server endpoints return an error without changing this shell. Clipboard content is parsed as data and never evaluated.",
		Example: "  kcm paste\n  # Or paste the command produced by kcm copy with Cmd+V, then press Enter.",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !shell {
				return fmt.Errorf("shell integration required: add eval \"$(kcm init zsh)\" (or bash) to your startup file")
			}
			if cmd.Flags().Changed("settings") {
				return fmt.Errorf("kcm paste uses the copied settings path; omit --settings")
			}
			if !cmd.Flags().Changed("selection") {
				content, err := readClipboard()
				if err != nil {
					return err
				}
				text := strings.TrimSpace(string(content))
				if !strings.HasPrefix(text, selectionPrefix) {
					return fmt.Errorf("%s", invalidSelection)
				}
				token = strings.TrimPrefix(text, selectionPrefix)
			}
			selection, err := decodeSelection(token)
			if err != nil {
				return err
			}
			if err := selectionDeadline(selection.ExpiresAt, time.Now()); err != nil {
				return err
			}
			s, err := loadSettings(selection.Settings)
			if err != nil {
				return fmt.Errorf("copied settings are unavailable: %w", err)
			}
			cs, err := s.contexts(selection.Profile)
			if err != nil {
				return err
			}
			c, err := selectContext(cs, selection.Context, selection.File)
			if err != nil {
				return err
			}
			if c.Server != selection.Server {
				return fmt.Errorf("copied context's server has changed; run kcm copy again in the source shell")
			}
			if err := s.activateContext(cmd.OutOrStdout(), selection.Profile, c, time.Now(), true); err != nil {
				return err
			}
			// Preserve the source deadline exactly, including an untimed selection.
			emit(cmd.OutOrStdout(), "KCM_EXPIRES_AT", selection.ExpiresAt)
			fmt.Fprintf(cmd.ErrOrStderr(), "Selected profile %s · context %s from the copied selection.\n", selection.Profile, selection.Context)
			return nil
		},
	}
	cmd.Flags().StringVar(&token, "selection", "", "Encoded selection from the command generated by kcm copy")
	cmd.Flags().MarkHidden("selection")
	return cmd
}
