package kcm

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/tools/clientcmd"
)

func currentProfile() string {
	if p := os.Getenv("KCM_PROFILE"); p != "" {
		return p
	}
	return "local"
}
func quote(s string) string             { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func emit(w io.Writer, key, val string) { fmt.Fprintf(w, "export %s=%s\n", key, quote(val)) }

func reset(w io.Writer, profile string) {
	emit(w, "KCM_PROFILE", profile)
	emit(w, "KUBECONFIG", "/dev/null")
	fmt.Fprintln(w, "unset KCM_CONTEXT KCM_FILE KCM_EXPIRES_AT KCM_PREVIOUS_CONTEXT KCM_PREVIOUS_FILE")
}

func choose(header string, rows []string) (int, error) {
	if len(rows) == 0 {
		return 0, fmt.Errorf("nothing available to select")
	}
	if _, err := exec.LookPath("fzf"); err != nil {
		return 0, fmt.Errorf("fzf is required for interactive selection; supply a name or install fzf")
	}
	lines := make([]string, len(rows))
	for i, r := range rows {
		lines[i] = fmt.Sprintf("%d\t%s", i, r)
	}
	cmd := exec.Command("fzf", "--no-multi", "--delimiter=\t", "--with-nth=2..", "--layout=reverse", "--height=70%", "--border", "--header="+header, "--prompt=› ")
	// User fzf options must not add shell previews, alter records, or select many.
	cmd.Env = filteredEnv("FZF_DEFAULT_OPTS", "FZF_DEFAULT_OPTS_FILE", "FZF_DEFAULT_COMMAND")
	cmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
	cmd.Stderr = os.Stderr
	b, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("selection cancelled")
	}
	n, err := strconv.Atoi(strings.SplitN(strings.TrimSpace(string(b)), "\t", 2)[0])
	if err != nil || n < 0 || n >= len(rows) {
		return 0, fmt.Errorf("invalid selection")
	}
	return n, nil
}

func filteredEnv(names ...string) []string {
	var out []string
	for _, e := range os.Environ() {
		skip := false
		for _, n := range names {
			if strings.HasPrefix(e, n+"=") {
				skip = true
			}
		}
		if !skip {
			out = append(out, e)
		}
	}
	return out
}

func chooseContext(cs []Context) (Context, error) {
	rows := make([]string, len(cs))
	for i, c := range cs {
		rows[i] = fmt.Sprintf("%s  │  %s  │  %s", c.Name, c.Server, c.File)
	}
	i, err := choose("Context · "+currentProfile(), rows)
	if err != nil {
		return Context{}, err
	}
	return cs[i], nil
}

func (s *Settings) switchContext(w io.Writer, name, file string, now time.Time) error {
	cs, err := s.contexts(currentProfile())
	if err != nil {
		return err
	}
	var c Context
	if name == "-" {
		name = os.Getenv("KCM_PREVIOUS_CONTEXT")
		file = os.Getenv("KCM_PREVIOUS_FILE")
		if name == "" {
			return fmt.Errorf("no previous context")
		}
	}
	if name == "" {
		c, err = chooseContext(cs)
	} else {
		c, err = selectContext(cs, name, file)
	}
	if err != nil {
		return err
	}
	if err := validName(c.File); err != nil {
		return err
	}
	expiry := ""
	if d := s.timeout(currentProfile(), c.File); d > 0 {
		deadline := now.Add(d).Unix()
		if old, err := strconv.ParseInt(os.Getenv("KCM_EXPIRES_AT"), 10, 64); err == nil && old > now.Unix() && old < deadline {
			deadline = old
		}
		expiry = strconv.FormatInt(deadline, 10)
	}
	emit(w, "KCM_SETTINGS", s.Path)
	emit(w, "KCM_PREVIOUS_CONTEXT", os.Getenv("KCM_CONTEXT"))
	emit(w, "KCM_PREVIOUS_FILE", os.Getenv("KCM_FILE"))
	emit(w, "KCM_CONTEXT", c.Name)
	emit(w, "KCM_FILE", c.File)
	emit(w, "KUBECONFIG", c.File)
	emit(w, "KCM_EXPIRES_AT", expiry)
	return nil
}

func (s *Settings) active() (Context, error) {
	cs, err := s.contexts(currentProfile())
	if err != nil {
		return Context{}, err
	}
	if os.Getenv("KCM_CONTEXT") == "" {
		return Context{}, fmt.Errorf("select a context first: kcm context")
	}
	return selectContext(cs, os.Getenv("KCM_CONTEXT"), os.Getenv("KCM_FILE"))
}

func (s *Settings) namespace(name string, create bool, out io.Writer) error {
	c, err := s.active()
	if err != nil {
		return err
	}
	if name == "" && !create {
		cmd := exec.Command("kubectl", "--kubeconfig", c.File, "--context", c.Name, "get", "namespaces", "-o", "jsonpath={range .items[*]}{.metadata.name}{\"\\n\"}{end}")
		cmd.Stderr = os.Stderr
		b, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("list namespaces: %w", err)
		}
		rows := strings.Fields(string(b))
		rows = append(rows, "＋ Create new namespace…")
		i, err := choose("Namespace · "+currentProfile()+" · "+c.Name, rows)
		if err != nil {
			return err
		}
		if i == len(rows)-1 {
			create = true
		} else {
			name = rows[i]
		}
	}
	if name == "" && create {
		w, err := newWizard()
		if err != nil {
			return err
		}
		defer w.file.Close()
		name, err = w.input("New namespace to create in "+currentProfile()+" / "+c.Name+" (blank cancels)", "")
		if err != nil {
			return err
		}
		if name == "" {
			return fmt.Errorf("namespace creation cancelled")
		}
	}
	if problems := validation.IsDNS1123Label(name); len(problems) > 0 {
		return fmt.Errorf("invalid namespace %q: %s", name, strings.Join(problems, "; "))
	}
	if create {
		cmd := exec.Command("kubectl", "--kubeconfig", c.File, "--context", c.Name, "create", "namespace", name)
		cmd.Stdout, cmd.Stderr = out, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("create namespace %q in context %q: %w; namespace selection unchanged", name, c.Name, err)
		}
	}
	if err := saveNamespace(c, name); err != nil {
		if create {
			return fmt.Errorf("namespace %q was created, but selecting it failed: %w", name, err)
		}
		return err
	}
	return nil
}

func saveNamespace(c Context, name string) error {
	config, err := readConfig(c.File)
	if err != nil {
		return err
	}
	ctx := config.Contexts[c.Name]
	if ctx == nil {
		return fmt.Errorf("context %q is no longer in %s", c.Name, c.File)
	}
	ctx.Namespace = name
	b, err := clientcmd.Write(*config)
	if err != nil {
		return err
	}
	// Replace the target atomically so the profile entry remains a symlink.
	return atomicWrite(canonical(c.File), b, true)
}
