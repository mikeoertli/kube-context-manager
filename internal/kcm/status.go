package kcm

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func printSelectionCommand(load func() (*Settings, error), file bool) *cobra.Command {
	name, alias, subject := "print-context-name", "ctx", "context name"
	if file {
		name, alias, subject = "print-kubeconfig", "kc", "kubeconfig file path"
	}
	return &cobra.Command{
		Use: name, Aliases: []string{alias}, Short: "Print this shell's selected " + subject,
		Long:    "Print only the selected " + subject + " followed by a newline, for use in scripts. Requires a valid KCM selection; if none is selected, exits with an error and no stdout. Does not fall back to a global context or /dev/null, change the selection, or contact the cluster.",
		Example: "  kcm " + alias, Args: cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			c, err := s.active()
			if err != nil {
				return err
			}
			value := c.Name
			if file {
				value = c.File
			}
			fmt.Fprintln(cmd.OutOrStdout(), value)
			return nil
		},
	}
}

func statusCommand(load func() (*Settings, error)) *cobra.Command {
	return &cobra.Command{Use: "status", Short: "Show this shell's selection and timeout in a readable summary", Args: cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			profile := currentProfile()
			dir, err := s.dir(profile)
			if err != nil {
				return err
			}
			name, file, namespace, expiry := "Not selected", "Not selected", "—", "Not active"
			selected := os.Getenv("KCM_CONTEXT") != ""
			if selected {
				c, err := s.active()
				if err != nil {
					return err
				}
				name, file, namespace = c.Name, c.File, c.Namespace
				if namespace == "" {
					namespace = "default"
				}
				expiry = "No timeout"
				if deadline, err := strconv.ParseInt(os.Getenv("KCM_EXPIRES_AT"), 10, 64); err == nil {
					remaining := time.Until(time.Unix(deadline, 0))
					if remaining <= 0 {
						expiry = "Expired"
					} else {
						expiry = remaining.Round(time.Second).String() + " remaining · expires " + time.Unix(deadline, 0).Format("Jan 2 15:04 MST")
					}
				}
			}
			out := cmd.OutOrStdout()
			style := func(text, code string) string { return text }
			if colorOutput(out) {
				style = func(text, code string) string { return "\x1b[" + code + "m" + text + "\x1b[0m" }
			}
			fmt.Fprintf(out, "\n  %s\n  %s\n", style("KCM session", "1;36"), style("───────────", "2"))
			for _, row := range [][2]string{{"Profile", s.Profiles[profile].Emoji + " " + profile}, {"Context", name}, {"Namespace", namespace}, {"Kubeconfig", file}, {"Directory", dir}, {"Timeout", expiry}} {
				fmt.Fprintf(out, "  %s %s\n", style(fmt.Sprintf("%-11s", row[0]), "2"), row[1])
			}
			if selected {
				fmt.Fprintln(out, "\n  Namespace changes affect other shells using this context.")
			} else {
				fmt.Fprintln(out, "\n  Select a context with kcm context.")
			}
			fmt.Fprintln(out)
			return nil
		},
	}
}

func colorOutput(out io.Writer) bool {
	if _, disabled := os.LookupEnv("NO_COLOR"); disabled || os.Getenv("TERM") == "dumb" {
		return false
	}
	file, ok := out.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}
