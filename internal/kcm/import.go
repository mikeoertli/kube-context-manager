package kcm

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/spf13/cobra"
	api "k8s.io/client-go/tools/clientcmd/api"
)

type importFile struct {
	path     string
	modified time.Time
}

func importFilename(name string) bool {
	name = strings.ToLower(name)
	return strings.Contains(name, ".kubeconfig") ||
		(filepath.Ext(name) == "" && (strings.Contains(name, "kube") || strings.Contains(name, "config")))
}

func importFiles(dirs []string, matching bool) ([]importFile, error) {
	var files []importFile
	seen := map[string]bool{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("browse %s: %w", dir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || (matching && !importFilename(entry.Name())) {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			if validName(path) != nil {
				continue
			}
			path, err = expandPath(path)
			if err != nil {
				continue
			}
			if seen[path] {
				continue
			}
			info, err := os.Stat(path)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			files = append(files, importFile{path, info.ModTime()})
			seen[path] = true
		}
	}
	sortImportFiles(files)
	return files, nil
}

func sortImportFiles(files []importFile) {
	sort.Slice(files, func(i, j int) bool {
		if files[i].modified.Equal(files[j].modified) {
			return files[i].path < files[j].path
		}
		return files[i].modified.After(files[j].modified)
	})
}

func defaultImportDirs(home string) []string {
	return []string{filepath.Join(home, "Desktop"), filepath.Join(home, "Downloads")}
}

func importSource(path string, selectFile, noPrompt bool) (string, error) {
	matching := false
	var dirs []string
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dirs, matching = defaultImportDirs(home), true
	} else {
		var err error
		path, err = expandPath(path)
		if err != nil {
			return "", err
		}
		info, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		if !info.IsDir() {
			if !info.Mode().IsRegular() {
				return "", fmt.Errorf("source must be a regular file or directory")
			}
			return path, nil
		}
		dirs = []string{path}
		selectFile = true
	}
	files, err := importFiles(dirs, matching)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", fmt.Errorf("no candidate kubeconfig files found; supply --file PATH or --dir DIR")
	}
	i := 0
	if selectFile && !noPrompt {
		rows := make([]string, len(files))
		for n, f := range files {
			rows[n] = f.modified.Format("2006-01-02 15:04:05") + "  │  " + f.path
		}
		i, err = chooseOrdered("Kubeconfig to import · newest first · type to search", rows)
		if err != nil {
			return "", err
		}
	}
	return files[i].path, nil
}

func importCommand(load func() (*Settings, error), shell bool) *cobra.Command {
	o := &installOptions{ImportReadability: "auto"}
	var file, dir string
	var selectFile, readable, skipReadable, importCopy, clipboard, importOnly bool
	cmd := &cobra.Command{
		Use: "import [path]", Short: "Import a kubeconfig, suggesting the newest download and readable names",
		Long:    "With no source, use the newest filename match in ~/Desktop and ~/Downloads: a name containing .kubeconfig, or an extensionless name containing kube or config (case insensitive). --select opens a searchable picker for those matches. A supplied directory opens a searchable list of its regular files, newest first; --no-prompt uses the newest directly. A supplied file bypasses discovery. --clipboard reads kubeconfig content without clearing the clipboard; relative credential paths resolve from the current directory. Selected content is validated before import. Choose a destination profile and filename, and optionally make messy labels readable. --readable forces name updates; --skip-readable preserves labels. With --no-prompt, omitted labels are preserved unless --readable is supplied. File imports move by default, archiving the original before removing it after a verified import. --import-copy retains the source file. Moving requires every source context; use --all-contexts or --import-copy when selecting from a multi-context file. Existing destinations are never overwritten. Successful imports select the destination profile and imported context for this shell, starting its configured timeout. --import-only preserves the shell selection. With --all-contexts, select the source current-context if valid, otherwise the first context by name. Shell integration is required unless using --import-only or --dry-run.",
		Example: "  kcm import\n  kcm import --select\n  kcm import --dir ~/Downloads --profile dev\n  kcm import --file ./download.yaml --profile dev --name api-dev --readable --no-prompt\n  kcm import ./download.yaml --profile qa --name api-qa --import-copy --skip-readable --no-prompt\n  kcm import --clipboard --profile dev --name api-dev --readable\n  kcm import ./download.yaml --profile dev --import-only --no-prompt",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 && (file != "" || dir != "" || clipboard) {
				return fmt.Errorf("use a positional path, --file, --dir, or --clipboard, not more than one")
			}
			if !shell && !importOnly && !o.DryRun {
				return fmt.Errorf("shell integration required to select the imported context: add eval \"$(kcm init zsh)\" (or bash) to your startup file, or use --import-only")
			}
			s, err := load()
			if err != nil {
				return err
			}
			selectionOut := cmd.OutOrStdout()
			if shell {
				// Only generated shell statements may enter the shell's eval channel.
				cmd.SetOut(cmd.ErrOrStderr())
				defer cmd.SetOut(selectionOut)
			}
			if !importOnly {
				o.OnInstalled = func(c Context) error {
					if err := s.activateContext(selectionOut, o.Profile, c, time.Now(), true); err != nil {
						return err
					}
					fmt.Fprintf(cmd.OutOrStdout(), "Selected profile %s · context %s for this shell.\n", o.Profile, c.Name)
					return nil
				}
			}
			if readable {
				o.ImportReadability = "force"
			}
			if skipReadable {
				o.ImportReadability = "skip"
			}
			o.Move, o.Copy = !importCopy && !clipboard, importCopy || clipboard
			if clipboard {
				path, cleanup, err := clipboardSource()
				if err != nil {
					return err
				}
				defer cleanup()
				o.SourceBase, err = os.Getwd()
				if err != nil {
					return err
				}
				o.SourceLabel = "clipboard"
				fmt.Fprintln(cmd.OutOrStdout(), "Source: clipboard")
				return s.install(cmd, path, o)
			}
			path := file
			if dir != "" {
				path = dir
			}
			if len(args) > 0 {
				path = args[0]
			}
			if file != "" || dir != "" {
				resolved, err := expandPath(path)
				if err != nil {
					return err
				}
				info, err := os.Stat(resolved)
				if err != nil {
					return err
				}
				if file != "" && !info.Mode().IsRegular() {
					return fmt.Errorf("--file must name a regular file")
				}
				if dir != "" && !info.IsDir() {
					return fmt.Errorf("--dir must name a directory")
				}
			}
			path, err = importSource(path, selectFile, o.NoPrompt)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Source:", path)
			return s.install(cmd, path, o)
		},
	}
	addInstallFlags(cmd, o, load)
	f := cmd.Flags()
	f.StringVar(&file, "file", "", "Explicit source file (bypasses automatic discovery)")
	f.StringVar(&dir, "dir", "", "Browse this directory's files, newest first (no recursion)")
	f.StringVar(&o.Filename, "name", "", "Destination filename and suggested base for readable labels")
	f.BoolVar(&selectFile, "select", false, "Search Desktop/Downloads filename matches rather than choosing the newest")
	f.BoolVar(&readable, "readable", false, "Make selected cluster/user/context labels readable; --name supplies the base")
	f.BoolVar(&skipReadable, "skip-readable", false, "Preserve cluster/user/context labels without readability prompts")
	f.BoolVar(&importCopy, "import-copy", false, "Keep the source file instead of moving it (file imports move by default)")
	f.BoolVar(&importOnly, "import-only", false, "Import without changing this shell's profile or context")
	f.BoolVar(&clipboard, "clipboard", false, "Read kubeconfig content from the clipboard without clearing it")
	f.Lookup("no-prompt").Usage = "Do not prompt; preserve names unless --readable or rename flags are supplied"
	cmd.MarkFlagsMutuallyExclusive("file", "dir")
	for _, flag := range []string{"file", "dir", "select"} {
		cmd.MarkFlagsMutuallyExclusive("clipboard", flag)
	}
	cmd.MarkFlagsMutuallyExclusive("name", "filename")
	cmd.MarkFlagsMutuallyExclusive("readable", "skip-readable")
	cmd.MarkFlagsMutuallyExclusive("readable", "all-contexts")
	cmd.MarkFlagsMutuallyExclusive("select", "no-prompt")
	for _, flag := range []string{"rename-cluster", "rename-user", "rename-context"} {
		cmd.MarkFlagsMutuallyExclusive("skip-readable", flag)
	}
	cmd.MarkFlagFilename("file")
	cmd.MarkFlagDirname("dir")
	for _, flag := range []string{"name", "filename", "rename-cluster", "rename-user", "rename-context", "namespace"} {
		cmd.RegisterFlagCompletionFunc(flag, cobra.NoFileCompletions)
	}
	return cmd
}

var generatedName = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|^[0-9a-f]{24,}$`)

func messyImportName(name string) bool {
	if len(name) > 48 || generatedName.MatchString(name) {
		return true
	}
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' && r != '.' {
			return true
		}
	}
	return false
}

func readableImportBase(name, profile string) string {
	base := name
	switch strings.ToLower(filepath.Ext(name)) {
	case ".yaml", ".yml", ".json", ".kubeconfig":
		base = strings.TrimSuffix(name, filepath.Ext(name))
	}
	if strings.Trim(base, ".-_") == "" || messyImportName(base) {
		return profile + "-cluster"
	}
	return base
}

func (s *Settings) prepareImportNames(cmd *cobra.Command, w *wizard, config *api.Config, o *installOptions) error {
	if o.Filename == "" {
		// The original source basename is supplied by install before this helper.
		return fmt.Errorf("import name could not be determined")
	}
	if w != nil && !cmd.Flags().Changed("name") && !cmd.Flags().Changed("filename") {
		name, err := w.input("Import name (destination filename)", o.Filename)
		if err != nil {
			return err
		}
		o.Filename = name
	}
	if o.All {
		if o.ImportReadability == "force" {
			return fmt.Errorf("--readable requires a single context; use --context NAME")
		}
		return nil
	}
	ctx := config.Contexts[o.Context]
	explicit := cmd.Flags().Changed("rename-cluster") || cmd.Flags().Changed("rename-user") || cmd.Flags().Changed("rename-context")
	want := o.ImportReadability == "force" || explicit
	if o.ImportReadability == "auto" && !explicit && w != nil &&
		(messyImportName(o.Context) || messyImportName(ctx.Cluster) || messyImportName(ctx.AuthInfo) || messyImportName(o.Filename)) {
		fmt.Fprintf(w.file, "Existing cluster: %s\nExisting user: %s\nExisting context: %s\n", ctx.Cluster, ctx.AuthInfo, o.Context)
		answer, err := w.input("Names look generated or complex. Make them readable? (yes/no)", "yes")
		if err != nil {
			return err
		}
		if answer != "yes" && answer != "no" {
			return fmt.Errorf("answer yes or no to the readability prompt")
		}
		want = answer == "yes"
	}
	if !want || o.ImportReadability == "skip" {
		return nil
	}
	base := readableImportBase(o.Filename, o.Profile)
	for _, field := range []struct {
		flag, label, def string
		dst              *string
	}{
		{"rename-cluster", "Readable cluster name", base, &o.ClusterName},
		{"rename-user", "Readable user name", base + "-user", &o.UserName},
		{"rename-context", "Readable context name", base, &o.ContextName},
	} {
		if field.flag == "rename-user" && ctx.AuthInfo == "" {
			continue
		}
		if cmd.Flags().Changed(field.flag) {
			continue
		}
		// Explicit rename flags alone preserve any omitted names in scripts.
		if w == nil && o.ImportReadability != "force" {
			continue
		}
		value := field.def
		if w != nil {
			var err error
			value, err = w.input(field.label, field.def)
			if err != nil {
				return err
			}
		}
		*field.dst = value
	}
	return nil
}
