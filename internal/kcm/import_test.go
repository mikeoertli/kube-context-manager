package kcm

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/tools/clientcmd"
)

func TestImportDiscoveryAndOrdering(t *testing.T) {
	home := t.TempDir()
	dirs := defaultImportDirs(home)
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	for _, file := range []struct {
		dir, name string
		age       time.Duration
	}{
		{dirs[0], "old.kubeconfig", time.Hour},
		{dirs[1], "new-kube-config", time.Minute},
		{dirs[1], "README.md", 0},
		{dirs[0], "other.KUBECONFIG.download", 2 * time.Hour},
		{dirs[0], "kube.yaml", 0},
	} {
		path := filepath.Join(file.dir, file.name)
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, now.Add(-file.age), now.Add(-file.age)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dirs[0], "directory.kubeconfig"), 0700); err != nil {
		t.Fatal(err)
	}
	files, err := importFiles(append(dirs, filepath.Join(home, "missing"), dirs[0]), true)
	if err != nil || len(files) != 3 {
		t.Fatalf("files=%v err=%v", files, err)
	}
	for i, name := range []string{"new-kube-config", "old.kubeconfig", "other.KUBECONFIG.download"} {
		if filepath.Base(files[i].path) != name {
			t.Fatalf("wrong order: %v", files)
		}
	}
	all, err := importFiles(dirs, false)
	if err != nil || len(all) != 5 {
		t.Fatalf("explicit directory files=%v err=%v", all, err)
	}
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"config", true}, {"kubeconfig", true}, {"prod-kube", true}, {"prod.KUBECONFIG", true},
		{"config.yaml", false}, {"notes.txt", false}, {"random", false},
	} {
		if importFilename(tc.name) != tc.want {
			t.Fatalf("filename %q", tc.name)
		}
	}
}

func TestImportNamesAndSourcePreservation(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		flags                  []string
		context, cluster, user string
	}{
		{"force", []string{"--readable"}, "api-dev", "api-dev", "api-dev-user"},
		{"skip", []string{"--skip-readable"}, "arn:example:opaque", "cluster", "user"},
		{"auto scripted preserves", nil, "arn:example:opaque", "cluster", "user"},
		{"explicit labels", []string{"--rename-context", "chosen"}, "chosen", "cluster", "user"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := fixture(t)
			source := filepath.Join(t.TempDir(), "download.kubeconfig")
			writeConfig(t, source, "https://dev.invalid", "arn:example:opaque")
			original, _ := os.ReadFile(source)
			args := []string{"import", "--file", source, "--profile", "dev", "--name", "api-dev", "--import-copy", "--no-prompt"}
			if _, err := run(t, append(args, tc.flags...)...); err != nil {
				t.Fatal(err)
			}
			got, err := readConfig(filepath.Join(s.Root, "dev", "api-dev"))
			if err != nil {
				t.Fatal(err)
			}
			ctx := got.Contexts[tc.context]
			if ctx == nil || ctx.Cluster != tc.cluster || ctx.AuthInfo != tc.user || ctx.Namespace != "original" || got.CurrentContext != tc.context {
				t.Fatalf("unexpected names: %+v", got)
			}
			after, _ := os.ReadFile(source)
			if !bytes.Equal(original, after) {
				t.Fatal("import modified source")
			}
			if _, err := run(t, args...); err == nil {
				t.Fatal("overwrote existing destination")
			}
		})
	}
}

func TestImportLatestDirectoryAndDryRun(t *testing.T) {
	s := fixture(t)
	dir := t.TempDir()
	older, newer := filepath.Join(dir, "a.yaml"), filepath.Join(dir, "z.yaml")
	writeConfig(t, older, "https://old.invalid", "old")
	writeConfig(t, newer, "https://new.invalid", "new")
	now := time.Now()
	os.Chtimes(older, now.Add(-time.Hour), now.Add(-time.Hour))
	os.Chtimes(newer, now, now)
	args := []string{"import", "--dir", dir, "--profile", "qa", "--name", "qa", "--skip-readable", "--no-prompt"}
	out, err := run(t, append(args, "--dry-run")...)
	if err != nil || !strings.Contains(out, newer) {
		t.Fatalf("wrong latest: %s %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(s.Root, "qa")); !os.IsNotExist(err) {
		t.Fatal("dry-run created destination")
	}
	if _, err := run(t, args...); err != nil {
		t.Fatal(err)
	}
	got, err := readConfig(filepath.Join(s.Root, "qa", "qa"))
	if err != nil || got.CurrentContext != "new" {
		t.Fatalf("wrong source: %+v %v", got, err)
	}
	if _, err := os.Stat(newer); !os.IsNotExist(err) {
		t.Fatal("default import retained source")
	}
	if _, err := os.Stat(older); err != nil {
		t.Fatal("import removed an unselected file")
	}
	files, _ := filepath.Glob(filepath.Join(s.Root, "archive", "qa.original-*"))
	if len(files) != 1 {
		t.Fatalf("missing move backup: %v", files)
	}
}

func TestImportMoveAndCopySafeguards(t *testing.T) {
	s := fixture(t)
	source := filepath.Join(t.TempDir(), "source.yaml")
	writeConfig(t, source, "https://dev.invalid", "one", "two")
	original, _ := os.ReadFile(source)
	if _, err := run(t, "import", source, "--profile", "dev", "--name", "partial", "--context", "one", "--no-prompt"); err == nil || !strings.Contains(err.Error(), "--import-copy") {
		t.Fatalf("partial move: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Root, "dev", "partial")); !os.IsNotExist(err) {
		t.Fatal("published a partial move")
	}
	if _, err := run(t, "import", source, "--profile", "dev", "--name", "partial", "--context", "one", "--import-copy", "--no-prompt"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "import", source, "--profile", "dev", "--name", "all", "--all-contexts", "--no-prompt"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatal("full import did not move source")
	}
	backups, _ := filepath.Glob(filepath.Join(s.Root, "archive", "all.original-*"))
	if len(backups) != 1 {
		t.Fatalf("missing original backup: %v", backups)
	}
	backup, _ := os.ReadFile(backups[0])
	if !bytes.Equal(original, backup) {
		t.Fatal("archived an altered original")
	}

	link := filepath.Join(t.TempDir(), "link.yaml")
	if err := os.Symlink(backups[0], link); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "import", link, "--profile", "dev", "--name", "linked", "--all-contexts", "--no-prompt"); err == nil || !strings.Contains(err.Error(), "--import-copy") {
		t.Fatalf("symlink move: %v", err)
	}
	if _, err := run(t, "import", link, "--profile", "dev", "--name", "linked", "--all-contexts", "--import-copy", "--no-prompt"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("copy changed source symlink")
	}
}

func TestImportValidationAndReadabilityDefaults(t *testing.T) {
	s := fixture(t)
	source := filepath.Join(t.TempDir(), "source.yaml")
	writeConfig(t, source, "https://remote.invalid", "old")
	for _, flags := range [][]string{
		{"--readable", "--skip-readable"}, {"--skip-readable", "--rename-context", "new"},
		{"--name", "new", "--filename", "other"}, {"--name", "../escape"},
		{"--readable", "--all-contexts"}, {"--select", "--no-prompt"},
	} {
		args := []string{"import", "--file", source, "--profile", "dev", "--no-prompt"}
		if _, err := run(t, append(args, flags...)...); err == nil {
			t.Fatalf("accepted flags: %v", flags)
		}
	}
	if _, err := run(t, "import", source, "--skip-readable", "--no-prompt"); err == nil {
		t.Fatal("remote source imported without a profile")
	}
	if _, err := os.Stat(s.Root); !os.IsNotExist(err) {
		t.Fatal("invalid input created files")
	}
	for _, tc := range []struct {
		name  string
		messy bool
	}{
		{"api-dev", false}, {"reader", false}, {"arn:example:cluster/dev", true}, {"opaque/label", true},
		{"12345678-1234-1234-1234-123456789abc", true}, {strings.Repeat("a", 64), true},
	} {
		if messyImportName(tc.name) != tc.messy {
			t.Fatalf("readability detection: %s", tc.name)
		}
	}
	if readableImportBase("api.dev.kubeconfig", "dev") != "api.dev" || readableImportBase("api.dev", "dev") != "api.dev" || readableImportBase("arn:long:name", "qa") != "qa-cluster" {
		t.Fatal("invalid readable base")
	}
}

func TestImportFilePickerRetainsOrdering(t *testing.T) {
	fixture(t)
	dir := t.TempDir()
	older, newer := filepath.Join(dir, "a.kubeconfig"), filepath.Join(dir, "z.kubeconfig")
	writeConfig(t, older, "https://old.invalid", "old")
	writeConfig(t, newer, "https://new.invalid", "new")
	os.Chtimes(older, time.Unix(100, 0), time.Unix(100, 0))
	os.Chtimes(newer, time.Unix(200, 0), time.Unix(200, 0))
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "args")
	t.Setenv("IMPORT_TEST_ARGS", log)
	stub := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$IMPORT_TEST_ARGS\"\nhead -n 1\n"
	if err := os.WriteFile(filepath.Join(bin, "fzf"), []byte(stub), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	got, err := importSource(dir, true, false)
	if err != nil || got != newer {
		t.Fatalf("picker: %q %v", got, err)
	}
	args, _ := os.ReadFile(log)
	if !strings.Contains(string(args), "--no-sort") {
		t.Fatal("fzf may reorder files after a query")
	}
}

func TestImportActivation(t *testing.T) {
	for _, mode := range []string{"renamed", "all", "all fallback", "only", "dry run", "failure"} {
		t.Run(mode, func(t *testing.T) {
			s := fixture(t)
			source := filepath.Join(t.TempDir(), "source.yaml")
			config := writeConfig(t, source, "https://prod.invalid", "zebra", "alpha")
			if mode == "all fallback" {
				config.CurrentContext = ""
				content, err := clientcmd.Write(*config)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(source, content, 0600); err != nil {
					t.Fatal(err)
				}
			}
			var out, messages bytes.Buffer
			cmd := newCommand("test", true)
			cmd.SetOut(&out)
			cmd.SetErr(&messages)
			args := []string{"import", source, "--profile", "prod", "--name", "new", "--import-copy", "--no-prompt"}
			expected := "renamed"
			switch mode {
			case "all", "all fallback":
				args = append(args, "--all-contexts")
				expected = "zebra"
				if mode == "all fallback" {
					expected = "alpha"
				}
			default:
				args = append(args, "--context", "zebra", "--rename-context", expected)
			}
			switch mode {
			case "only":
				args = append(args, "--import-only")
			case "dry run":
				args = append(args, "--dry-run")
			case "failure":
				args = append(args, "--name", "../bad")
			}
			cmd.SetArgs(args)
			err := cmd.Execute()
			if mode == "failure" {
				if err == nil {
					t.Fatal("accepted invalid destination")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if mode == "only" || mode == "dry run" || mode == "failure" {
				if out.Len() != 0 {
					t.Fatalf("selection changed: %s", out.String())
				}
				return
			}
			for _, statement := range []string{"export KCM_PROFILE='prod'", "export KCM_CONTEXT=" + quote(expected), "export KCM_FILE=" + quote(filepath.Join(s.Root, "prod", "new")), "unset KCM_CONTEXT KCM_FILE KCM_EXPIRES_AT KCM_PREVIOUS_CONTEXT KCM_PREVIOUS_FILE"} {
				if !strings.Contains(out.String(), statement) {
					t.Fatalf("missing %s in %s", statement, out.String())
				}
			}
			if strings.Contains(out.String(), "Installed") || strings.Contains(out.String(), "Copy ") {
				t.Fatal("messages entered shell eval output")
			}
			if !strings.Contains(messages.String(), "Selected profile prod") {
				t.Fatal("missing selection confirmation")
			}
			deadline := ""
			for _, line := range strings.Split(out.String(), "\n") {
				if strings.HasPrefix(line, "export KCM_EXPIRES_AT=") {
					deadline = strings.Trim(strings.TrimPrefix(line, "export KCM_EXPIRES_AT="), "'")
				}
			}
			seconds, err := strconv.ParseInt(deadline, 10, 64)
			if err != nil || seconds < time.Now().Add(7*time.Hour).Unix() || seconds > time.Now().Add(9*time.Hour).Unix() {
				t.Fatalf("invalid import timeout: %q", deadline)
			}
		})
	}
}

func TestImportWithoutShell(t *testing.T) {
	s := fixture(t)
	source := filepath.Join(t.TempDir(), "source.yaml")
	writeConfig(t, source, "https://dev.invalid", "dev")
	args := []string{"import", source, "--profile", "dev", "--no-prompt"}
	cmd := NewCommand("test")
	cmd.SetArgs(args)
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "--import-only") {
		t.Fatalf("missing shell integration error: %v", err)
	}
	if _, err := os.Stat(s.Root); !os.IsNotExist(err) {
		t.Fatal("failed activation requirement wrote files")
	}
	cmd = NewCommand("test")
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs(append(args, "--import-only"))
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
}
