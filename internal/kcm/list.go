package kcm

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"k8s.io/client-go/tools/clientcmd"
)

type profileContexts struct {
	name     string
	dir      string
	contexts []Context
}

func (s *Settings) allProfileContexts() ([]profileContexts, error) {
	if err := s.validateRoot(); err != nil {
		return nil, err
	}
	names := []string{"local"}
	for _, name := range s.names() {
		if name != "local" {
			names = append(names, name)
		}
	}
	var groups []profileContexts
	for _, name := range names {
		dir, err := s.dir(name)
		if err != nil {
			return nil, err
		}
		contexts, err := scanDir(dir)
		if err != nil {
			return nil, fmt.Errorf("profile %s: %w", name, err)
		}
		groups = append(groups, profileContexts{name, dir, contexts})
	}
	return groups, nil
}

func printProfileContexts(out io.Writer, s *Settings, groups []profileContexts, globalName, globalFile string) error {
	fmt.Fprintln(out, "* = global (standard client); > = this shell's KCM selection")
	for _, group := range groups {
		selected := ""
		if group.name == currentProfile() {
			selected = " [current profile]"
		}
		fmt.Fprintf(out, "\n%s %s%s — %s\n", s.Profiles[group.name].Emoji, group.name, selected, group.dir)
		if len(group.contexts) == 0 {
			fmt.Fprintln(out, "  (no contexts)")
			continue
		}
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "  \tCONTEXT\tNAMESPACE\tSERVER\tFILE")
		for _, c := range group.contexts {
			marker := ""
			if globalName != "" && c.Name == globalName && canonical(c.File) == canonical(globalFile) {
				marker += "*"
			}
			if group.name == currentProfile() && c.Name == os.Getenv("KCM_CONTEXT") && os.Getenv("KCM_FILE") != "" && canonical(c.File) == canonical(os.Getenv("KCM_FILE")) {
				marker += ">"
			}
			ns := c.Namespace
			if ns == "" {
				ns = "default"
			}
			fmt.Fprintf(w, "%2s\t%s\t%s\t%s\t%s\n", marker, c.Name, ns, c.Server, c.File)
		}
		if err := w.Flush(); err != nil {
			return err
		}
	}
	return nil
}

func listCommand(load func() (*Settings, error)) *cobra.Command {
	return &cobra.Command{Use: "list", Short: "List discovered contexts grouped by profile, with current selections marked",
		Long:    "List contexts in every configured profile, including empty profiles. Show namespaces, servers, and source paths. Mark the effective global context used by standard clients with '*' and this shell's KCM selection with '>'. Global selection follows KUBECONFIG merge precedence, or ~/.kube/config when unset; saved defaults in inactive files are not marked. Reads local configs only; does not switch profiles, modify configs, run credential helpers, or query permissions.",
		Example: "  kcm list", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			groups, err := s.allProfileContexts()
			if err != nil {
				return err
			}
			name, file, err := globalContext()
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "kcm: global selection unavailable: %v\n", err)
			}
			return printProfileContexts(cmd.OutOrStdout(), s, groups, name, file)
		},
	}
}

// Match the standard client's merge precedence without client initialization or
// legacy-config migration: listing must never authenticate or write files.
func globalContext() (string, string, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.MigrationRules = nil
	rules.WarnIfAllMissing = false
	if os.Getenv(clientcmd.RecommendedConfigPathEnvVar) == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", "", err
		}
		rules.Precedence = []string{filepath.Join(home, ".kube", "config")}
	}
	config, err := rules.Load()
	if err != nil {
		return "", "", err
	}
	if config.CurrentContext == "" {
		return "", "", nil
	}
	c := config.Contexts[config.CurrentContext]
	if c == nil {
		return "", "", fmt.Errorf("current-context %q has no context definition", config.CurrentContext)
	}
	path, err := filepath.Abs(c.LocationOfOrigin)
	if err != nil {
		return "", "", err
	}
	return config.CurrentContext, path, nil
}
