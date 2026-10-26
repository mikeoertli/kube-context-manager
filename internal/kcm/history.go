package kcm

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

const historyLimit = 20

type historyEntry struct {
	Profile string `json:"profile"`
	Name    string `json:"context"`
	File    string `json:"file"`
}

func recentHistory() []historyEntry {
	var entries []historyEntry
	raw := os.Getenv("KCM_HISTORY")
	if raw != "" && json.Unmarshal([]byte(raw), &entries) == nil {
		return normalizeHistory(entries)
	}
	// Upgrade the existing two-way toggle without losing its previous selection.
	profile := os.Getenv("KCM_PREVIOUS_PROFILE")
	if profile == "" {
		profile = currentProfile()
	}
	return normalizeHistory([]historyEntry{{profile, os.Getenv("KCM_PREVIOUS_CONTEXT"), os.Getenv("KCM_PREVIOUS_FILE")}})
}

func normalizeHistory(entries []historyEntry) []historyEntry {
	var result []historyEntry
	seen := make(map[historyEntry]bool)
	for _, entry := range entries {
		if entry.Profile == "" || entry.Name == "" || entry.File == "" {
			continue
		}
		if validName(entry.Profile) != nil || validName(entry.Name) != nil || validName(entry.File) != nil {
			continue
		}
		entry.File = configPath(entry.File)
		if seen[entry] {
			continue
		}
		seen[entry] = true
		result = append(result, entry)
		if len(result) == historyLimit {
			break
		}
	}
	return result
}

func emitHistory(w io.Writer, entries []historyEntry) {
	entries = normalizeHistory(entries)
	if len(entries) == 0 {
		clearPrevious(w)
		return
	}
	content, _ := json.Marshal(entries) // Only strings: encoding cannot fail.
	emit(w, "KCM_HISTORY", string(content))
	emit(w, "KCM_PREVIOUS_PROFILE", entries[0].Profile)
	emit(w, "KCM_PREVIOUS_CONTEXT", entries[0].Name)
	emit(w, "KCM_PREVIOUS_FILE", entries[0].File)
}

// Store distinct outgoing selections, newest first, excluding the new target.
// Profile-only changes preserve the previous context until another is selected.
func savePrevious(w io.Writer, profile, name, file string) {
	entries := recentHistory()
	current := historyEntry{currentProfile(), os.Getenv("KCM_CONTEXT"), os.Getenv("KCM_FILE")}
	target := historyEntry{profile, name, configPath(file)}
	if current.Name != "" && current.File != "" {
		current.File = configPath(current.File)
		if current != target {
			entries = append([]historyEntry{current}, entries...)
		}
	}
	var previous []historyEntry
	for _, entry := range entries {
		if entry != target {
			previous = append(previous, entry)
		}
	}
	emitHistory(w, previous)
}

func historyCommand(load func() (*Settings, error), shell bool) *cobra.Command {
	var list bool
	cmd := &cobra.Command{
		Use: "history [number]", Short: "Choose a recent profile/context, or print shell-local history",
		Long:    "Keep the last 20 distinct previous profile/context/file selections in this shell, newest first. Without arguments, search and select with fzf. A number selects that row directly; --list prints numbered history without switching or accessing clusters. The current selection is excluded. Entries are validated against current settings and kubeconfigs before switching; missing or invalid targets leave the shell unchanged. Selection follows normal timeout rules. kcm - toggles the most recent entry. New shells, reset, clear, and expiry clear history. No history file or credentials are stored.",
		Example: "  kcm history\n  kcm history --list\n  kcm history 2\n  kcm -",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			entries := recentHistory()
			if list {
				if len(args) != 0 {
					return fmt.Errorf("--list cannot be combined with a history number")
				}
				out := cmd.OutOrStdout()
				if shell {
					out = cmd.ErrOrStderr()
				} // Keep tables out of eval in older shell integrations.
				if len(entries) == 0 {
					fmt.Fprintln(out, "No previous selections in this shell.")
					return nil
				}
				w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
				fmt.Fprintln(w, "#\tPROFILE\tCONTEXT\tFILE")
				for i, entry := range entries {
					fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", i+1, entry.Profile, entry.Name, entry.File)
				}
				return w.Flush()
			}
			if !shell {
				return fmt.Errorf("shell integration required: add eval \"$(kcm init zsh)\" (or bash) to your startup file")
			}
			if len(entries) == 0 {
				return fmt.Errorf("no previous selections in this shell")
			}
			index := 0
			if len(args) != 0 {
				number, err := strconv.Atoi(args[0])
				if err != nil || number < 1 || number > len(entries) {
					return fmt.Errorf("history number must be between 1 and %d", len(entries))
				}
				index = number - 1
			} else {
				rows := make([]string, len(entries))
				for i, entry := range entries {
					rows[i] = fmt.Sprintf("%d  │  %s  │  %s  │  %s", i+1, entry.Profile, entry.Name, entry.File)
				}
				var err error
				index, err = chooseOrdered("Recent selections · newest first · type to search", rows)
				if err != nil {
					return err
				}
			}
			entry := entries[index]
			s, err := load()
			if err != nil {
				return err
			}
			cs, err := s.contexts(entry.Profile)
			if err != nil {
				return err
			}
			c, err := selectContext(cs, entry.Name, entry.File)
			if err != nil {
				return err
			}
			return s.activateContext(cmd.OutOrStdout(), entry.Profile, c, time.Now(), entry.Profile != currentProfile())
		},
	}
	cmd.Flags().BoolVar(&list, "list", false, "Print recent selections without switching")
	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
		var numbers []string
		if len(args) == 0 {
			for i := range recentHistory() {
				number := strconv.Itoa(i + 1)
				if strings.HasPrefix(number, prefix) {
					numbers = append(numbers, number)
				}
			}
		}
		return numbers, cobra.ShellCompDirectiveNoFileComp
	}
	return cmd
}

func renameHistory(w io.Writer, oldName, newName, target string) {
	if oldName == newName {
		return
	}
	entries := recentHistory()
	changed := false
	for i := range entries {
		if entries[i].Name == oldName && canonical(entries[i].File) == target {
			entries[i].Name = newName
			changed = true
		}
	}
	if changed {
		emitHistory(w, entries)
	}
}
