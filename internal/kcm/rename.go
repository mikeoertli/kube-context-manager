package kcm

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/tools/clientcmd"
	api "k8s.io/client-go/tools/clientcmd/api"
)

type renameOptions struct {
	Context, ClusterName, UserName, ContextName, Namespace string
	NoPrompt, DryRun                                       bool
}

func renameCommand(load func() (*Settings, error), shell bool) *cobra.Command {
	o := &renameOptions{}
	cmd := &cobra.Command{
		Use: "rename [path]", Aliases: []string{"make-readable"}, Short: "Rename kubeconfig entries in place, with an original backup",
		Long:    "Give a kubeconfig's cluster, user, and context readable names. Defaults to this shell's selected file/context; otherwise choose a file interactively. For a supplied file, use --context to select an entry, or default to its valid current-context, then the first context by name. Other contexts are preserved, and references to renamed clusters/users are updated throughout the file. Set the file's current-context to the resulting context. Preserve namespaces unless --namespace is supplied. Refuse name collisions and archive the exact original under kube_root/archive before writing. Symlinks remain links. No cluster access, yq, or gum is required.",
		Example: "  kcm rename\n  kcm rename ./download.yaml\n  kcm rename ./download.yaml --rename-cluster dev --rename-user dev-reader --rename-context dev --no-prompt\n  kcm rename --rename-context api-dev --no-prompt --dry-run",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			path := ""
			if len(args) > 0 {
				path = args[0]
			}
			out := cmd.OutOrStdout()
			if shell {
				out = cmd.ErrOrStderr()
			}
			return s.renameConfig(cmd, out, path, o, shell)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.Context, "context", "", "Source context (default: shell selection, current-context, or first by name)")
	f.StringVar(&o.ClusterName, "rename-cluster", "", "New cluster display name; updates all references in this file")
	f.StringVar(&o.UserName, "rename-user", "", "New user display name; updates all references in this file")
	f.StringVar(&o.ContextName, "rename-context", "", "New context display name")
	f.StringVar(&o.Namespace, "namespace", "", "Selected context's namespace (default: preserve; empty clears it)")
	f.BoolVar(&o.NoPrompt, "no-prompt", false, "Apply supplied choices without prompting; preserve omitted names")
	f.BoolVar(&o.DryRun, "dry-run", false, "Validate and show the plan without creating a backup or writing")
	cmd.RegisterFlagCompletionFunc("context", func(_ *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
		path := os.Getenv("KCM_FILE")
		if len(args) > 0 {
			path = args[0]
		}
		if path == "" {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		path, err := expandPath(path)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		config, err := readConfig(path)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var names []string
		for name := range config.Contexts {
			if strings.HasPrefix(name, prefix) {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		return names, cobra.ShellCompDirectiveNoFileComp
	})
	for _, flag := range []string{"rename-cluster", "rename-user", "rename-context", "namespace"} {
		cmd.RegisterFlagCompletionFunc(flag, cobra.NoFileCompletions)
	}
	return cmd
}

func (s *Settings) renamePath(path string, w *wizard) (string, error) {
	if path != "" {
		return expandPath(path)
	}
	if os.Getenv("KCM_CONTEXT") != "" && os.Getenv("KCM_FILE") != "" {
		return expandPath(os.Getenv("KCM_FILE"))
	}
	if w == nil {
		return "", fmt.Errorf("supply a kubeconfig path or select a context first")
	}
	contexts, err := s.contexts(currentProfile())
	if err != nil {
		return "", err
	}
	seen := map[string]bool{}
	var files []importFile
	for _, c := range contexts {
		if !seen[c.File] {
			info, err := os.Stat(c.File)
			if err != nil {
				return "", err
			}
			files = append(files, importFile{c.File, info.ModTime()})
			seen[c.File] = true
		}
	}
	sortImportFiles(files)
	if len(files) > 0 {
		rows := make([]string, len(files))
		for i, f := range files {
			rows[i] = f.modified.Format("2006-01-02 15:04:05") + "  │  " + f.path
		}
		i, err := chooseOrdered("Kubeconfig to rename · newest first", append(rows, "[Enter another path]"))
		if err != nil {
			return "", err
		}
		if i < len(files) {
			return files[i].path, nil
		}
	}
	path, err = w.input("Kubeconfig path (blank cancels)", "")
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", fmt.Errorf("rename cancelled")
	}
	return expandPath(path)
}

func (s *Settings) renameConfig(cmd *cobra.Command, out io.Writer, path string, o *renameOptions, shell bool) error {
	var w *wizard
	var err error
	if !o.NoPrompt {
		w, err = newWizard()
		if err != nil {
			return err
		}
		defer w.file.Close()
	}
	path, err = s.renamePath(path, w)
	if err != nil {
		return err
	}
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(target)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("kubeconfig must be a regular file: %s", path)
	}
	original, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	config, err := readConfig(target)
	if err != nil {
		return err
	}
	if o.Context == "" {
		if os.Getenv("KCM_FILE") != "" && canonical(os.Getenv("KCM_FILE")) == target && os.Getenv("KCM_CONTEXT") != "" {
			o.Context = os.Getenv("KCM_CONTEXT")
		} else if config.Contexts[config.CurrentContext] != nil {
			o.Context = config.CurrentContext
		} else {
			names := make([]string, 0, len(config.Contexts))
			for name := range config.Contexts {
				names = append(names, name)
			}
			sort.Strings(names)
			o.Context = names[0]
		}
	}
	ctx := config.Contexts[o.Context]
	if ctx == nil {
		return fmt.Errorf("source context %q not found", o.Context)
	}
	oldCluster, oldUser, oldContext := ctx.Cluster, ctx.AuthInfo, o.Context
	oldNamespace, oldCurrent := ctx.Namespace, config.CurrentContext
	if w != nil {
		for _, field := range []struct {
			flag, label, def string
			dst              *string
		}{
			{"rename-cluster", "Cluster name", oldCluster, &o.ClusterName},
			{"rename-user", "User name", oldUser, &o.UserName},
			{"rename-context", "Context name", oldContext, &o.ContextName},
		} {
			if field.flag == "rename-user" && oldUser == "" {
				continue
			}
			if !cmd.Flags().Changed(field.flag) {
				*field.dst, err = w.input(field.label, field.def)
				if err != nil {
					return err
				}
			}
		}
	}
	if o.ClusterName == "" && !cmd.Flags().Changed("rename-cluster") {
		o.ClusterName = oldCluster
	}
	if o.UserName == "" && !cmd.Flags().Changed("rename-user") {
		o.UserName = oldUser
	}
	if o.ContextName == "" && !cmd.Flags().Changed("rename-context") {
		o.ContextName = oldContext
	}
	if err := renameEntries(config, o, cmd.Flags().Changed("namespace")); err != nil {
		return err
	}
	changed := oldCluster != o.ClusterName || oldUser != o.UserName || oldContext != o.ContextName ||
		oldNamespace != config.Contexts[o.ContextName].Namespace || oldCurrent != config.CurrentContext
	fmt.Fprintf(out, "Rename entries in %s\n  Cluster: %s → %s\n  User:    %s → %s\n  Context: %s → %s\n  Namespace: %s\n", path, oldCluster, o.ClusterName, oldUser, o.UserName, oldContext, o.ContextName, config.Contexts[o.ContextName].Namespace)
	if !changed {
		fmt.Fprintln(out, "Already uses these names; no changes.")
		return nil
	}
	fmt.Fprintf(out, "Original will be backed up under %s\n", filepath.Join(s.Root, "archive"))
	if o.DryRun {
		return nil
	}
	if w != nil {
		answer, err := w.input("Apply changes? (yes/no)", "no")
		if err != nil {
			return err
		}
		if answer != "yes" {
			return fmt.Errorf("rename cancelled")
		}
	}
	unchanged := func() error {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		latest, err := os.ReadFile(target)
		if err != nil {
			return err
		}
		if resolved != target || !bytes.Equal(latest, original) {
			return fmt.Errorf("source changed during rename; no changes applied")
		}
		return nil
	}
	if err := unchanged(); err != nil {
		return err
	}
	b, err := clientcmd.Write(*config)
	if err != nil {
		return err
	}
	backup := filepath.Join(s.Root, "archive", filepath.Base(path)+".original-"+time.Now().UTC().Format("20060102T150405.000000000Z"))
	if err := atomicWrite(backup, original, false); err != nil {
		return fmt.Errorf("could not back up original; source unchanged: %w", err)
	}
	if err := unchanged(); err != nil {
		return fmt.Errorf("%w; original backup: %s", err, backup)
	}
	if err := atomicWrite(target, b, true); err != nil {
		return fmt.Errorf("could not update kubeconfig; original backup: %s: %w", backup, err)
	}
	if shell {
		for _, selection := range [][2]string{{"KCM_CONTEXT", "KCM_FILE"}, {"KCM_PREVIOUS_CONTEXT", "KCM_PREVIOUS_FILE"}} {
			if os.Getenv(selection[0]) == oldContext && os.Getenv(selection[1]) != "" && canonical(os.Getenv(selection[1])) == target {
				emit(cmd.OutOrStdout(), selection[0], o.ContextName)
			}
		}
		renameHistory(cmd.OutOrStdout(), oldContext, o.ContextName, target)
	}
	fmt.Fprintf(out, "Updated kubeconfig: %s\nOriginal backup: %s\nCompare: diff -u -- %s %s\nRestore: cp -- %s %s\n", path, backup, quote(backup), quote(target), quote(backup), quote(target))
	if !shell && os.Getenv("KCM_CONTEXT") == oldContext && os.Getenv("KCM_FILE") != "" && canonical(os.Getenv("KCM_FILE")) == target && oldContext != o.ContextName {
		fmt.Fprintf(out, "Select the renamed context in your shell: kcm context %s --file %s\n", quote(o.ContextName), quote(os.Getenv("KCM_FILE")))
	}
	return nil
}

func renameEntries(config *api.Config, o *renameOptions, namespace bool) error {
	ctx := config.Contexts[o.Context]
	if ctx == nil {
		return fmt.Errorf("source context %q not found", o.Context)
	}
	for _, name := range []string{o.ClusterName, o.ContextName} {
		if err := validName(name); err != nil {
			return err
		}
	}
	if ctx.AuthInfo == "" && o.UserName != "" {
		return fmt.Errorf("cannot rename an absent user")
	}
	if ctx.AuthInfo != "" {
		if err := validName(o.UserName); err != nil {
			return err
		}
	}
	if o.ClusterName != ctx.Cluster && config.Clusters[o.ClusterName] != nil {
		return fmt.Errorf("cluster name %q already exists", o.ClusterName)
	}
	if o.UserName != ctx.AuthInfo && config.AuthInfos[o.UserName] != nil {
		return fmt.Errorf("user name %q already exists", o.UserName)
	}
	if o.ContextName != o.Context && config.Contexts[o.ContextName] != nil {
		return fmt.Errorf("context name %q already exists", o.ContextName)
	}
	if namespace && o.Namespace != "" {
		if errors := validation.IsDNS1123Label(o.Namespace); len(errors) > 0 {
			return fmt.Errorf("invalid namespace %q: %v", o.Namespace, errors)
		}
	}
	oldCluster, oldUser := ctx.Cluster, ctx.AuthInfo
	if o.ClusterName != oldCluster {
		config.Clusters[o.ClusterName] = config.Clusters[oldCluster]
		delete(config.Clusters, oldCluster)
	}
	if o.UserName != oldUser {
		config.AuthInfos[o.UserName] = config.AuthInfos[oldUser]
		delete(config.AuthInfos, oldUser)
	}
	for _, other := range config.Contexts {
		if other.Cluster == oldCluster {
			other.Cluster = o.ClusterName
		}
		if oldUser != "" && other.AuthInfo == oldUser {
			other.AuthInfo = o.UserName
		}
	}
	if namespace {
		ctx.Namespace = o.Namespace
	}
	if o.ContextName != o.Context {
		config.Contexts[o.ContextName] = ctx
		delete(config.Contexts, o.Context)
	}
	config.CurrentContext = o.ContextName
	return nil
}
