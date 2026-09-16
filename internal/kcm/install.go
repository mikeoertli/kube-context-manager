package kcm

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"k8s.io/client-go/tools/clientcmd"
	api "k8s.io/client-go/tools/clientcmd/api"
)

type installOptions struct {
	Profile, Context, ClusterName, UserName, ContextName, Namespace, Filename string
	All, Move, Copy, NoPrompt, DryRun                                         bool
}

type wizard struct {
	file   *os.File
	reader *bufio.Reader
}

func newWizard() (*wizard, error) {
	f, e := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if e != nil {
		return nil, fmt.Errorf("interactive input needs a terminal; supply command arguments and flags instead")
	}
	return &wizard{f, bufio.NewReader(f)}, nil
}
func (w *wizard) input(label, def string) (string, error) {
	fmt.Fprintf(w.file, "%s [%s]: ", label, def)
	s, e := w.reader.ReadString('\n')
	if e != nil {
		return "", fmt.Errorf("input cancelled: %w", e)
	}
	s = strings.TrimSpace(s)
	if s == "" {
		s = def
	}
	return s, nil
}

func installCommand(load func() (*Settings, error)) *cobra.Command {
	o := &installOptions{}
	c := &cobra.Command{Use: "install <path>", Short: "Import a kubeconfig, with optional readable names and copy/move", Args: cobra.ExactArgs(1),
		Long:    "Import selected contexts into a profile. Missing choices are prompted. Remote configs require --profile with --no-prompt. Existing destinations are never overwritten. Certificate/key references are embedded; token-file and exec paths are preserved as absolute paths. --move requires importing every source context.",
		Example: "  kcm install ./download.yaml\n  kcm install ./download.yaml --profile prod --rename-context customer-a --no-prompt\n  kcm install ./local.yaml --no-prompt --dry-run",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, e := load()
			if e != nil {
				return e
			}
			return s.install(cmd, args[0], o)
		}}
	f := c.Flags()
	f.StringVar(&o.Profile, "profile", "", "Destination profile")
	f.StringVar(&o.Context, "context", "", "Source context to import (required when ambiguous)")
	f.BoolVar(&o.All, "all-contexts", false, "Import every context into the destination profile")
	f.StringVar(&o.ClusterName, "rename-cluster", "", "New cluster display name")
	f.StringVar(&o.UserName, "rename-user", "", "New user display name")
	f.StringVar(&o.ContextName, "rename-context", "", "New context display name")
	f.StringVar(&o.Namespace, "namespace", "", "Installed namespace (default: preserve)")
	f.StringVar(&o.Filename, "filename", "", "Destination basename (default: source basename)")
	f.BoolVar(&o.Move, "move", false, "Delete source only after a complete, verified import")
	f.BoolVar(&o.Copy, "copy", false, "Keep source (default)")
	f.BoolVar(&o.NoPrompt, "no-prompt", false, "Do not prompt; preserve omitted names")
	f.BoolVar(&o.DryRun, "dry-run", false, "Validate and print the plan without writing")
	c.MarkFlagsMutuallyExclusive("move", "copy")
	c.MarkFlagsMutuallyExclusive("context", "all-contexts")
	c.RegisterFlagCompletionFunc("profile", profileCompletion(load))
	return c
}

func (s *Settings) install(cmd *cobra.Command, path string, o *installOptions) error {
	path, err := expandPath(path)
	if err != nil {
		return err
	}
	sourceBytes, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	config, err := readConfig(path)
	if err != nil {
		return err
	}
	var w *wizard
	if !o.NoPrompt {
		w, err = newWizard()
		if err != nil {
			return err
		}
		defer w.file.Close()
	}
	names := make([]string, 0, len(config.Contexts))
	for n := range config.Contexts {
		names = append(names, n)
	}
	sort.Strings(names)
	if !o.All && o.Context == "" {
		if len(names) == 1 {
			o.Context = names[0]
		} else if w != nil {
			i, e := choose("Context to import", append(names, "[All contexts]"))
			if e != nil {
				return e
			}
			if i == len(names) {
				o.All = true
			} else {
				o.Context = names[i]
			}
		} else {
			return fmt.Errorf("multiple contexts: specify --context or --all-contexts")
		}
	}
	if !o.All && config.Contexts[o.Context] == nil {
		return fmt.Errorf("source context %q not found", o.Context)
	}
	if o.All && (o.ClusterName != "" || o.UserName != "" || o.ContextName != "") {
		return fmt.Errorf("rename flags require a single --context")
	}
	if !o.All {
		config.CurrentContext = o.Context
		if err := api.MinifyConfig(config); err != nil {
			return err
		}
	}
	remote := false
	for _, ctx := range config.Contexts {
		if !s.local(config.Clusters[ctx.Cluster].Server) {
			remote = true
		}
	}
	if o.Profile == "" {
		if w == nil {
			if remote {
				return fmt.Errorf("non-local config requires --profile with --no-prompt")
			}
			o.Profile = "local"
		} else {
			profiles := s.names()
			rows := make([]string, len(profiles))
			suggest := "local"
			if remote {
				suggest = "other"
			}
			for i, p := range profiles {
				rows[i] = p
				if p == suggest {
					rows[i] += " (suggested)"
				}
			}
			i, e := choose("Destination profile", rows)
			if e != nil {
				return e
			}
			o.Profile = profiles[i]
		}
	}
	dir, err := s.dir(o.Profile)
	if err != nil {
		return err
	}
	if canonical(dir) == canonical(s.Root) && remote {
		return fmt.Errorf("non-local configs belong in a non-root profile; choose dev, qa, prod, other, or a custom profile")
	}
	if w != nil && !o.All {
		ctx := config.Contexts[o.Context]
		for _, field := range []struct {
			flag, label, def string
			dst              *string
		}{
			{"rename-cluster", "Cluster name", ctx.Cluster, &o.ClusterName}, {"rename-user", "User name", ctx.AuthInfo, &o.UserName}, {"rename-context", "Context name", o.Context, &o.ContextName}, {"namespace", "Namespace", ctx.Namespace, &o.Namespace},
		} {
			if !cmd.Flags().Changed(field.flag) {
				v, e := w.input(field.label, field.def)
				if e != nil {
					return e
				}
				*field.dst = v
			}
		}
	}
	if !o.All {
		ctx := config.Contexts[o.Context]
		if o.ClusterName != "" && o.ClusterName != ctx.Cluster {
			if e := validName(o.ClusterName); e != nil {
				return e
			}
			config.Clusters[o.ClusterName] = config.Clusters[ctx.Cluster]
			delete(config.Clusters, ctx.Cluster)
			ctx.Cluster = o.ClusterName
		}
		if o.UserName != "" && o.UserName != ctx.AuthInfo {
			if ctx.AuthInfo == "" {
				return fmt.Errorf("cannot rename an absent user")
			}
			if e := validName(o.UserName); e != nil {
				return e
			}
			config.AuthInfos[o.UserName] = config.AuthInfos[ctx.AuthInfo]
			delete(config.AuthInfos, ctx.AuthInfo)
			ctx.AuthInfo = o.UserName
		}
		if o.ContextName != "" && o.ContextName != o.Context {
			if e := validName(o.ContextName); e != nil {
				return e
			}
			config.Contexts[o.ContextName] = ctx
			delete(config.Contexts, o.Context)
			config.CurrentContext = o.ContextName
		}
	}
	if cmd.Flags().Changed("namespace") || o.Namespace != "" {
		if o.Namespace != "" {
			if e := validName(o.Namespace); e != nil {
				return e
			}
		}
		for _, ctx := range config.Contexts {
			ctx.Namespace = o.Namespace
		}
	}
	if o.Filename == "" {
		o.Filename = filepath.Base(path)
		if w != nil {
			v, e := w.input("Destination filename", o.Filename)
			if e != nil {
				return e
			}
			o.Filename = v
		}
	}
	if o.Filename == "." || strings.HasPrefix(o.Filename, ".") || filepath.Base(o.Filename) != o.Filename || strings.ContainsAny(o.Filename, "/:\\\n\r") {
		return fmt.Errorf("filename must be a visible basename without path separators")
	}
	if err := validName(o.Filename); err != nil {
		return err
	}
	if !discoverableFile(o.Filename) {
		return fmt.Errorf("filename is excluded from discovery; use --filename with a kubeconfig filename")
	}
	destination := filepath.Join(dir, o.Filename)
	if canonical(destination) == canonical(path) {
		return fmt.Errorf("source and destination are the same file")
	}
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("destination already exists: %s", destination)
	} else if !os.IsNotExist(err) {
		return err
	}
	if w != nil && !cmd.Flags().Changed("move") && !cmd.Flags().Changed("copy") {
		i, e := choose("Source file", []string{"Copy (keep source)", "Move (remove source after successful import)"})
		if e != nil {
			return e
		}
		o.Move = i == 1
	}
	if o.Move && len(names) != len(config.Contexts) {
		return fmt.Errorf("--move would discard unselected contexts; use --copy or --all-contexts")
	}
	if o.Move {
		info, e := os.Lstat(path)
		if e != nil {
			return e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("--move does not accept symlink sources; use --copy")
		}
	}
	// LoadFromFile records each object's origin. Resolve before relocating.
	if err := clientcmd.ResolveLocalPaths(config); err != nil {
		return err
	}
	for _, auth := range config.AuthInfos {
		if auth.Exec != nil && strings.Contains(auth.Exec.Command, "/") && !filepath.IsAbs(auth.Exec.Command) {
			auth.Exec.Command = filepath.Join(filepath.Dir(path), auth.Exec.Command)
		}
	}
	if err := api.FlattenConfig(config); err != nil {
		return fmt.Errorf("resolve referenced certificates/keys: %w", err)
	}
	if config.Contexts[config.CurrentContext] == nil && len(config.Contexts) > 0 {
		for _, n := range names {
			if config.Contexts[n] != nil {
				config.CurrentContext = n
				break
			}
		}
	}
	b, err := clientcmd.Write(*config)
	if err != nil {
		return err
	}
	verb := "Copy"
	if o.Move {
		verb = "Move"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s %s → %s\nProfile: %s\n", verb, path, destination, o.Profile)
	for n, ctx := range config.Contexts {
		fmt.Fprintf(cmd.OutOrStdout(), "  %s · cluster=%s · user=%s · namespace=%s\n", n, ctx.Cluster, ctx.AuthInfo, ctx.Namespace)
	}
	if o.DryRun {
		return nil
	}
	if w != nil {
		v, e := w.input("Install? (yes/no)", "no")
		if e != nil {
			return e
		}
		if v != "yes" {
			return fmt.Errorf("installation cancelled")
		}
	}
	if err := atomicWrite(destination, b, false); err != nil {
		return err
	}
	if _, err := readConfig(destination); err != nil {
		return fmt.Errorf("verification failed; source retained: %w", err)
	}
	if o.Move {
		// Check source has not changed while the user was in the wizard.
		latest, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		if string(latest) != string(sourceBytes) {
			return fmt.Errorf("installed, but source changed during import; source retained")
		}
		backup := filepath.Join(s.Root, "archive", o.Filename+".original")
		f, e := os.CreateTemp(filepath.Dir(destination), ".kcm-backup-*")
		if e != nil {
			return e
		}
		backupSuffix := filepath.Base(f.Name())
		f.Close()
		os.Remove(f.Name())
		backup += "-" + strings.TrimPrefix(backupSuffix, ".kcm-backup-")
		if e = atomicWrite(backup, sourceBytes, false); e != nil {
			return fmt.Errorf("installed but could not archive source; source retained: %w", e)
		}
		if e = os.Remove(path); e != nil {
			return fmt.Errorf("installed and archived but could not remove source: %w", e)
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Original archived:", backup)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Installed. Select it with kcm profile", o.Profile, "then kcm context.")
	return nil
}
