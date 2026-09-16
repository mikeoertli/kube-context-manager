package kcm

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

func NewCommand(version string) *cobra.Command { return newCommand(version, false) }

func newCommand(version string, shell bool) *cobra.Command {
	path := settingsPath()
	load := func() (*Settings, error) { return loadSettings(path) }
	needShell := func() error {
		if !shell {
			return fmt.Errorf("shell integration required: add eval \"$(kcm init zsh)\" (or bash) to your shell startup file")
		}
		return nil
	}
	root := &cobra.Command{Use: "kcm", Short: "Kubernetes profiles and contexts scoped to your shell", Long: "Choose Kubernetes profiles and contexts for this shell. Source kubeconfigs are shared; context selection uses environment variables and the kcmkubectl, kcmhelm, and kcmk9s shell wrappers. Expiry resets an interactive shell to local and cancels an Enter-submitted command at a stale prompt.", SilenceUsage: true, SilenceErrors: true, Args: cobra.NoArgs}
	root.PersistentFlags().StringVar(&path, "settings", path, "Settings file (also KCM_SETTINGS)")
	root.CompletionOptions.DisableDefaultCmd = true
	contextCmd := &cobra.Command{Use: "context [name|-]", Aliases: []string{"ctx"}, Short: "Select a context in this profile; omit name for fzf", Long: "Select a context for this shell without writing current-context to the source file. Use '-' to return to the previous context. Duplicate names require --file or interactive selection.", Args: cobra.MaximumNArgs(1), Example: "  kcm context\n  kcm ctx kind-local\n  kcm context customer-a --file ~/.kube/prod/customer.yaml"}
	var file string
	contextCmd.Flags().StringVar(&file, "file", "", "Disambiguate by source kubeconfig path")
	contextCmd.RunE = func(cmd *cobra.Command, args []string) error {
		if e := needShell(); e != nil {
			return e
		}
		s, e := load()
		if e != nil {
			return e
		}
		name := ""
		if len(args) > 0 {
			name = args[0]
		}
		if file != "" {
			file, e = expandPath(file)
			if e != nil {
				return e
			}
		}
		return s.switchContext(cmd.OutOrStdout(), name, file, time.Now())
	}
	contextCmd.ValidArgsFunction = contextCompletion(load)
	root.RunE = contextCmd.RunE
	root.AddCommand(contextCmd)
	profile := &cobra.Command{Use: "profile [name]", Short: "Change this shell's profile; omit name for fzf", Long: "Expose only the chosen profile's directory in the context selector. Switching profiles clears the selected context and timeout; select a context afterwards. New shells start local.", Args: cobra.MaximumNArgs(1), Example: "  kcm profile\n  kcm profile prod\n  kcm context customer-a", ValidArgsFunction: profileCompletion(load), RunE: func(cmd *cobra.Command, args []string) error {
		if e := needShell(); e != nil {
			return e
		}
		s, e := load()
		if e != nil {
			return e
		}
		if e = s.validateRoot(); e != nil {
			return e
		}
		name := ""
		if len(args) > 0 {
			name = args[0]
		} else {
			names := s.names()
			rows := make([]string, len(names))
			for i, n := range names {
				dir, _ := s.dir(n)
				timeout := "no timeout"
				if d := s.timeout(n, dir); d > 0 {
					timeout = d.String()
				}
				rows[i] = s.Profiles[n].Emoji + " " + n + "  │  " + dir + "  │  " + timeout
			}
			i, e := choose("Profile for this shell", rows)
			if e != nil {
				return e
			}
			name = names[i]
		}
		if _, e = s.dir(name); e != nil {
			return e
		}
		emit(cmd.OutOrStdout(), "KCM_SETTINGS", s.Path)
		reset(cmd.OutOrStdout(), name)
		return nil
	}}
	root.AddCommand(profile)
	var createNamespace bool
	namespace := &cobra.Command{Use: "namespace [name]", Aliases: []string{"ns"}, Short: "Choose a shared namespace, or create one and switch to it", Long: "Update the namespace in the selected source kubeconfig. Other shells using that same context will see the change. Without a name, list existing namespaces with fzf, including a 'Create new namespace…' choice. --create creates a namespace in the selected cluster before switching; omit its name to prompt. A name without --create only updates the config and does not contact the cluster. Failed creation or cancellation leaves the selection unchanged.", Args: cobra.MaximumNArgs(1), Example: "  kcm ns\n  kcm ns app\n  kcm ns feature-demo --create\n  kcm ns --create", RunE: func(cmd *cobra.Command, args []string) error {
		s, e := load()
		if e != nil {
			return e
		}
		n := ""
		if len(args) > 0 {
			n = args[0]
		}
		return s.namespace(n, createNamespace, cmd.OutOrStdout())
	}}
	namespace.Flags().BoolVar(&createNamespace, "create", false, "Create in the selected cluster, then switch (prompts for name if omitted)")
	root.AddCommand(namespace)
	var namesOnly bool
	contexts := &cobra.Command{Use: "contexts", Short: "List available contexts, servers, namespaces, and source files", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		s, e := load()
		if e != nil {
			return e
		}
		cs, e := s.contexts(currentProfile())
		if e != nil {
			return e
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		defer w.Flush()
		if !namesOnly {
			fmt.Fprintln(w, "CONTEXT\tSERVER\tNAMESPACE\tFILE")
		}
		for _, c := range cs {
			if namesOnly {
				fmt.Fprintln(w, c.Name)
			} else {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", c.Name, c.Server, c.Namespace, c.File)
			}
		}
		return nil
	}}
	contexts.Flags().BoolVar(&namesOnly, "names", false, "Print only context names")
	root.AddCommand(contexts)
	root.AddCommand(&cobra.Command{Use: "profiles", Short: "List profile directories and default timeouts", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		s, e := load()
		if e != nil {
			return e
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		defer w.Flush()
		fmt.Fprintln(w, "PROFILE\tDIRECTORY\tTIMEOUT")
		for _, n := range s.names() {
			d, _ := s.dir(n)
			timeout := "disabled"
			if t := s.timeout(n, d); t > 0 {
				timeout = t.String()
			}
			fmt.Fprintf(w, "%s %s\t%s\t%s\n", s.Profiles[n].Emoji, n, d, timeout)
		}
		return nil
	}})
	root.AddCommand(&cobra.Command{Use: "clear", Short: "Clear the selected context, keeping this shell's profile", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if e := needShell(); e != nil {
			return e
		}
		reset(cmd.OutOrStdout(), currentProfile())
		return nil
	}})
	root.AddCommand(&cobra.Command{Use: "renew", Short: "Explicitly restart the active context's configured timeout", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if e := needShell(); e != nil {
			return e
		}
		s, e := load()
		if e != nil {
			return e
		}
		c, e := s.active()
		if e != nil {
			return e
		}
		v := ""
		if d := s.timeout(currentProfile(), c.File); d > 0 {
			v = strconv.FormatInt(time.Now().Add(d).Unix(), 10)
		}
		emit(cmd.OutOrStdout(), "KCM_SETTINGS", s.Path)
		emit(cmd.OutOrStdout(), "KCM_EXPIRES_AT", v)
		return nil
	}})
	for _, name := range []string{"status", "prompt"} {
		name := name
		short := "Show profile, context, namespace, and expiry"
		if name == "prompt" {
			short = "Print compact profile/context information for Starship"
		}
		root.AddCommand(&cobra.Command{Use: name, Short: short, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
			s, e := load()
			if e != nil {
				return e
			}
			p := currentProfile()
			context := os.Getenv("KCM_CONTEXT")
			if context == "" {
				context = "—"
			}
			expiry := "disabled"
			if v, e := strconv.ParseInt(os.Getenv("KCM_EXPIRES_AT"), 10, 64); e == nil {
				d := time.Until(time.Unix(v, 0))
				if d <= 0 {
					expiry = "expired"
				} else {
					expiry = d.Round(time.Second).String() + " left"
				}
			}
			if name == "prompt" {
				suffix := ""
				if expiry != "disabled" {
					suffix = " · " + expiry
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s %s · %s%s", s.Profiles[p].Emoji, p, context, suffix)
				return nil
			}
			ns := "—"
			if os.Getenv("KCM_CONTEXT") != "" {
				c, e := s.active()
				if e != nil {
					return e
				}
				ns = c.Namespace
				if ns == "" {
					ns = "default"
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Profile:   %s\nContext:   %s\nNamespace: %s (shared)\nConfig:    %s\nTimeout:   %s\n", p, context, ns, os.Getenv("KUBECONFIG"), expiry)
			return nil
		}})
	}
	var quiet bool
	doctor := &cobra.Command{Use: "doctor", Short: "Validate settings, profile configs, root waivers, and fzf", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		s, e := load()
		if e != nil {
			return e
		}
		if e = s.validateRoot(); e != nil {
			return e
		}
		for _, n := range s.names() {
			d, _ := s.dir(n)
			if _, e := scanDir(d); e != nil {
				return fmt.Errorf("profile %s: %w", n, e)
			}
		}
		if !quiet {
			fmt.Fprintln(cmd.OutOrStdout(), "Settings and kubeconfigs: OK")
			if _, e = exec.LookPath("fzf"); e != nil {
				fmt.Fprintln(cmd.OutOrStdout(), "fzf: missing (named selections still work)")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "fzf: OK")
			}
		}
		return nil
	}}
	doctor.Flags().BoolVar(&quiet, "quiet", false, "Print errors only")
	root.AddCommand(doctor)
	root.AddCommand(installCommand(load))
	addWaivers(root, load)
	settings := &cobra.Command{Use: "settings", Short: "Locate or initialize TOML settings"}
	settings.AddCommand(&cobra.Command{Use: "path", Short: "Print the effective settings path", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		p, e := expandPath(path)
		if e != nil {
			return e
		}
		fmt.Fprintln(cmd.OutOrStdout(), p)
		return nil
	}})
	settings.AddCommand(&cobra.Command{Use: "init", Short: "Write the example settings; never overwrite an existing file", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		p, e := expandPath(path)
		if e != nil {
			return e
		}
		if e = atomicWrite(p, DefaultSettings, false); e != nil {
			return e
		}
		fmt.Fprintln(cmd.OutOrStdout(), p)
		return nil
	}})
	root.AddCommand(settings)
	root.AddCommand(&cobra.Command{Use: "init <zsh|bash>", Short: "Print shell integration (source it in your startup file)", Long: "Initialize shell-local selection and expiry hooks, plus kcmkubectl, kcmhelm, and kcmk9s functions that pass the selected context to their client. Standard client commands, aliases, and functions are preserved. Source this output in your shell startup file.", Args: cobra.ExactArgs(1), ValidArgs: []string{"zsh", "bash"}, Example: "  eval \"$(kcm init zsh)\"\n  eval \"$(kcm init bash)\"", RunE: func(cmd *cobra.Command, args []string) error {
		p, e := expandPath(path)
		if e != nil {
			return e
		}
		return shellInit(cmd.OutOrStdout(), args[0], p)
	}})
	root.AddCommand(&cobra.Command{Use: "completion <zsh|bash|fish|powershell>", Short: "Print shell completions, including profile/context names", Args: cobra.ExactArgs(1), ValidArgs: []string{"zsh", "bash", "fish", "powershell"}, RunE: func(cmd *cobra.Command, args []string) error {
		switch args[0] {
		case "zsh":
			return root.GenZshCompletion(cmd.OutOrStdout())
		case "bash":
			return root.GenBashCompletionV2(cmd.OutOrStdout(), true)
		case "fish":
			return root.GenFishCompletion(cmd.OutOrStdout(), true)
		case "powershell":
			return root.GenPowerShellCompletionWithDesc(cmd.OutOrStdout())
		default:
			return fmt.Errorf("unknown completion shell %q", args[0])
		}
	}})
	root.AddCommand(&cobra.Command{Use: "exec -- <kubectl|helm|k9s> [args...]", Short: "Run a supported client with the shell-selected context", Long: "Pass the selected context explicitly to kubectl, helm, or k9s. Useful in scripts where interactive shell functions are unavailable. This command does not enforce expiry or revoke access.", Args: cobra.MinimumNArgs(1), DisableFlagParsing: true, RunE: func(cmd *cobra.Command, args []string) error {
		if args[0] == "--help" || args[0] == "-h" {
			return cmd.Help()
		}
		if args[0] == "--" {
			args = args[1:]
		}
		if len(args) == 0 {
			return fmt.Errorf("specify kubectl, helm, or k9s")
		}
		flag := "--context"
		switch filepath.Base(args[0]) {
		case "helm":
			flag = "--kube-context"
		case "kubectl", "k9s":
		default:
			return fmt.Errorf("supported clients: kubectl, helm, k9s")
		}
		s, e := load()
		if e != nil {
			return e
		}
		c, e := s.active()
		if e != nil {
			return e
		}
		a := append([]string{flag, c.Name}, args[1:]...)
		child := exec.Command(args[0], a...)
		child.Env = append(filteredEnv("KUBECONFIG"), "KUBECONFIG="+c.File)
		child.Stdin = os.Stdin
		child.Stdout = cmd.OutOrStdout()
		child.Stderr = cmd.ErrOrStderr()
		return child.Run()
	}})
	root.AddCommand(&cobra.Command{Use: "version", Short: "Show the development version", Args: cobra.NoArgs, Run: func(cmd *cobra.Command, args []string) { fmt.Fprintln(cmd.OutOrStdout(), "kcm", version) }})
	if !shell {
		root.AddCommand(&cobra.Command{Use: "__shell", Hidden: true, DisableFlagParsing: true, RunE: func(cmd *cobra.Command, args []string) error {
			sub := newCommand(version, true)
			sub.SetArgs(args)
			sub.SetOut(cmd.OutOrStdout())
			sub.SetErr(cmd.ErrOrStderr())
			return sub.Execute()
		}})
	}
	return root
}

func profileCompletion(load func() (*Settings, error)) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
		s, e := load()
		if e != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		var out []string
		for _, n := range s.names() {
			if strings.HasPrefix(n, prefix) {
				out = append(out, n)
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}
func contextCompletion(load func() (*Settings, error)) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
		s, e := load()
		if e != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		cs, e := s.contexts(currentProfile())
		if e != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		var out []string
		for _, c := range cs {
			if strings.HasPrefix(c.Name, prefix) {
				out = append(out, c.Name+"\t"+filepath.Base(c.File))
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

func addWaivers(root *cobra.Command, load func() (*Settings, error)) {
	root.AddCommand(&cobra.Command{Use: "waivers", Short: "List non-local root waivers (context, source file, server)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		s, e := load()
		if e != nil {
			return e
		}
		ws, e := s.waivers()
		if e != nil {
			return e
		}
		for _, w := range ws {
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", w.Context, w.File, w.Server)
		}
		return nil
	}})
	for _, name := range []string{"waive", "unwaive"} {
		name := name
		var file string
		short := "Allow a non-local context at the kube root"
		if name == "unwaive" {
			short = "Remove a non-local root waiver"
		}
		c := &cobra.Command{Use: name + " <context>", Short: short, Long: short + ". Waivers are stored in kube_root/.kcm_waivers and bind to a context name, canonical file path, and exact server address. Use --file for duplicate names.", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			s, e := load()
			if e != nil {
				return e
			}
			ws, e := s.waivers()
			if e != nil {
				return e
			}
			if file != "" {
				file, e = expandPath(file)
				if e != nil {
					return e
				}
			}
			if name == "waive" {
				cs, e := scanDir(s.Root)
				if e != nil {
					return e
				}
				c, e := selectContext(cs, args[0], file)
				if e != nil {
					return e
				}
				if s.local(c.Server) {
					return fmt.Errorf("context is already local; no waiver needed")
				}
				for _, w := range ws {
					if w.Fingerprint == fingerprint(c) {
						return nil
					}
				}
				ws = append(ws, Waiver{c.Name, canonical(c.File), c.Server, fingerprint(c)})
			} else {
				kept := make([]Waiver, 0, len(ws))
				for _, w := range ws {
					if w.Context != args[0] || (file != "" && canonical(file) != w.File) {
						kept = append(kept, w)
					}
				}
				if len(kept) == len(ws) {
					return fmt.Errorf("waiver not found")
				}
				ws = kept
			}
			b, e := json.MarshalIndent(ws, "", "  ")
			if e != nil {
				return e
			}
			return atomicWrite(filepath.Join(s.Root, ".kcm_waivers"), append(b, '\n'), true)
		}}
		c.Flags().StringVar(&file, "file", "", "Disambiguate the source kubeconfig")
		root.AddCommand(c)
	}
}
