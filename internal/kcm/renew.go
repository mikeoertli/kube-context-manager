package kcm

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"
)

func renewCommand(load func() (*Settings, error), needShell func() error) *cobra.Command {
	var duration, extension time.Duration
	cmd := &cobra.Command{
		Use: "renew", Short: "Restart or extend this shell's active context timeout",
		Long:    "Without flags, restart the active context's configured timeout. --for sets a deadline from now; --extend adds time to an existing, unexpired deadline. Durations must be at least one second (for example 24h or 96h). These changes affect only this shell's selection, not settings, credentials, or other shells. Later context selections still apply the profile's normal timeout rules.",
		Example: "  kcm renew\n  kcm renew --for 24h\n  kcm renew --extend 24h", Args: cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := needShell(); err != nil {
				return err
			}
			for _, flag := range []string{"for", "extend"} {
				if cmd.Flags().Changed(flag) {
					d, _ := cmd.Flags().GetDuration(flag)
					if d < time.Second {
						return fmt.Errorf("--%s must be a duration of at least 1s", flag)
					}
				}
			}
			s, err := load()
			if err != nil {
				return err
			}
			c, err := s.active()
			if err != nil {
				return err
			}
			now := time.Now()
			base := now
			d := s.timeout(currentProfile(), c.File)
			if cmd.Flags().Changed("for") {
				d = duration
			}
			if cmd.Flags().Changed("extend") {
				old, err := strconv.ParseInt(os.Getenv("KCM_EXPIRES_AT"), 10, 64)
				if err != nil || old <= now.Unix() {
					return fmt.Errorf("no active timeout to extend; use kcm renew --for DURATION")
				}
				base = time.Unix(old, 0)
				d = extension
			}
			expiry := ""
			if d > 0 {
				deadline := base.Add(d).Unix()
				if deadline <= base.Unix() {
					return fmt.Errorf("timeout extension is too large")
				}
				expiry = strconv.FormatInt(deadline, 10)
			}
			emit(cmd.OutOrStdout(), "KCM_SETTINGS", s.Path)
			emit(cmd.OutOrStdout(), "KCM_EXPIRES_AT", expiry)
			s.emitPromptMetadata(cmd.OutOrStdout(), currentProfile())
			return nil
		},
	}
	cmd.Flags().DurationVar(&duration, "for", 0, "Set expiry this far from now, e.g. 24h")
	cmd.Flags().DurationVar(&extension, "extend", 0, "Add this much time to the active deadline, e.g. 24h")
	cmd.MarkFlagsMutuallyExclusive("for", "extend")
	for _, flag := range []string{"for", "extend"} {
		if err := cmd.RegisterFlagCompletionFunc(flag, func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
			return []string{"1h", "8h", "24h", "48h", "96h"}, cobra.ShellCompDirectiveNoFileComp
		}); err != nil {
			panic(err) // Both flags were registered above.
		}
	}
	return cmd
}
