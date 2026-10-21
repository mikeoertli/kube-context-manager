package kcm

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	api "k8s.io/client-go/tools/clientcmd/api"
)

func storeRenameFixture(t *testing.T, path string, config *api.Config) []byte {
	t.Helper()
	b, err := clientcmd.Write(*config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	return b
}

func renameBackups(t *testing.T, s *Settings) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(s.Root, "archive", "*"))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestRenameSharedReferencesAndBackups(t *testing.T) {
	s := fixture(t)
	path := filepath.Join(t.TempDir(), "download.yaml")
	config := writeConfig(t, path, "https://cluster.invalid", "alpha", "beta")
	config.CurrentContext = "beta"
	config.Clusters["cluster"].CertificateAuthority = "relative/ca.pem"
	config.AuthInfos["user"].TokenFile = "relative/token"
	config.AuthInfos["user"].Exec = &api.ExecConfig{Command: "./credential-helper", APIVersion: "client.authentication.k8s.io/v1", InteractiveMode: api.NeverExecInteractiveMode}
	config.Clusters["unused"] = &api.Cluster{Server: "https://unused.invalid"}
	config.Extensions = map[string]runtime.Object{"example": &runtime.Unknown{Raw: []byte(`{"preserve":"me"}`)}}
	original := storeRenameFixture(t, path, config)
	out, err := run(t, "rename", path, "--context", "alpha", "--rename-cluster", "api-dev", "--rename-user", "reader", "--rename-context", "readable", "--namespace", "app", "--no-prompt")
	if err != nil {
		t.Fatal(err)
	}
	got, err := readConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.CurrentContext != "readable" || got.Contexts["alpha"] != nil || got.Clusters["cluster"] != nil || got.AuthInfos["user"] != nil {
		t.Fatalf("old names retained: %+v", got)
	}
	for _, name := range []string{"readable", "beta"} {
		ctx := got.Contexts[name]
		if ctx.Cluster != "api-dev" || ctx.AuthInfo != "reader" {
			t.Fatalf("broken shared reference: %+v", ctx)
		}
	}
	if got.Contexts["readable"].Namespace != "app" || got.Contexts["beta"].Namespace != "original" {
		t.Fatal("namespace scope changed")
	}
	if got.Clusters["unused"] == nil || got.Extensions["example"] == nil || got.Clusters["api-dev"].CertificateAuthority != "relative/ca.pem" || got.AuthInfos["reader"].Exec.Command != "./credential-helper" || got.AuthInfos["reader"].TokenFile != "relative/token" || got.AuthInfos["reader"].Token != "fixture-only" {
		t.Fatal("lost data or resolved credential paths")
	}
	if strings.Contains(out, "fixture-only") || !strings.Contains(out, "Compare:") || !strings.Contains(out, "Restore:") {
		t.Fatalf("bad summary: %s", out)
	}
	backups := renameBackups(t, s)
	if len(backups) != 1 {
		t.Fatalf("backups: %v", backups)
	}
	backup, _ := os.ReadFile(backups[0])
	if !bytes.Equal(original, backup) {
		t.Fatal("backup did not preserve exact source bytes")
	}
	for _, file := range []string{path, backups[0]} {
		info, _ := os.Stat(file)
		if info.Mode().Perm() != 0600 {
			t.Fatalf("incorrect permissions: %s", file)
		}
	}
	beforeSecond, _ := os.ReadFile(path)
	if _, err := run(t, "rename", path, "--rename-context", "again", "--no-prompt"); err != nil {
		t.Fatal(err)
	}
	backups = renameBackups(t, s)
	if len(backups) != 2 {
		t.Fatalf("backup overwritten: %v", backups)
	}
	second, _ := os.ReadFile(backups[1])
	if !bytes.Equal(second, beforeSecond) {
		t.Fatal("second original not archived")
	}
	if _, err := run(t, "rename", path, "--no-prompt"); err != nil {
		t.Fatal(err)
	}
	if len(renameBackups(t, s)) != 2 {
		t.Fatal("no-op created a backup")
	}
}

func TestRenameRejectsCollisionsAndInvalidChoices(t *testing.T) {
	for _, args := range [][]string{
		{"--rename-cluster", "occupied"}, {"--rename-user", "occupied"}, {"--rename-context", "occupied"},
		{"--context", "missing"}, {"--rename-user", ""}, {"--rename-cluster", "bad\nname"}, {"--namespace", "Invalid_Name"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			s := fixture(t)
			path := filepath.Join(t.TempDir(), "source.yaml")
			config := writeConfig(t, path, "https://cluster.invalid", "selected", "occupied")
			config.Clusters["occupied"] = &api.Cluster{Server: "https://other.invalid"}
			config.AuthInfos["occupied"] = &api.AuthInfo{Token: "unrelated"}
			original := storeRenameFixture(t, path, config)
			out, err := run(t, append([]string{"rename", path, "--no-prompt"}, args...)...)
			if err == nil || strings.Contains(out, "export ") {
				t.Fatalf("invalid rename: %q %v", out, err)
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(original, after) || len(renameBackups(t, s)) != 0 {
				t.Fatal("failed validation changed files")
			}
		})
	}
}

func TestRenameCurrentContextFallbackAndAnonymousUser(t *testing.T) {
	for _, current := range []string{"", "missing", "beta"} {
		t.Run(current, func(t *testing.T) {
			fixture(t)
			path := filepath.Join(t.TempDir(), "source.yaml")
			config := writeConfig(t, path, "https://localhost", "alpha", "beta")
			config.CurrentContext = current
			config.Contexts["alpha"].AuthInfo = ""
			storeRenameFixture(t, path, config)
			if _, err := run(t, "make-readable", path, "--rename-context", "renamed", "--no-prompt"); err != nil {
				t.Fatal(err)
			}
			got, _ := readConfig(path)
			old := "alpha"
			if current == "beta" {
				old = "beta"
			}
			if got.Contexts[old] != nil || got.CurrentContext != "renamed" {
				t.Fatal("wrong context renamed")
			}
			if old == "alpha" && got.Contexts["renamed"].AuthInfo != "" {
				t.Fatal("added a user to an anonymous context")
			}
		})
	}
}

func TestRenameDryRunAndSymlinkSelection(t *testing.T) {
	s := fixture(t)
	target := filepath.Join(t.TempDir(), "target.yaml")
	writeConfig(t, target, "https://localhost", "one", "two")
	link := filepath.Join(t.TempDir(), "link.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(target)
	t.Setenv("KCM_CONTEXT", "two")
	t.Setenv("KCM_FILE", link)
	t.Setenv("KCM_PREVIOUS_CONTEXT", "two")
	t.Setenv("KCM_PREVIOUS_FILE", target)
	t.Setenv("KCM_EXPIRES_AT", "2000000000")
	out, err := run(t, "rename", "--rename-context", "readable", "--no-prompt", "--dry-run")
	if err != nil || strings.Contains(out, "export ") {
		t.Fatalf("dry-run: %s %v", out, err)
	}
	after, _ := os.ReadFile(target)
	if !bytes.Equal(original, after) || len(renameBackups(t, s)) != 0 {
		t.Fatal("dry-run wrote files")
	}
	out, err = run(t, "rename", "--rename-context", "readable", "--no-prompt")
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Lstat(link)
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("replaced the link")
	}
	got, _ := readConfig(target)
	if got.Contexts["readable"] == nil || got.Contexts["two"] != nil || got.Contexts["one"] == nil {
		t.Fatal("did not use shell-selected context")
	}
	for _, key := range []string{"KCM_CONTEXT", "KCM_PREVIOUS_CONTEXT"} {
		if !strings.Contains(out, "export "+key+"='readable'") {
			t.Fatalf("selection not refreshed: %s", out)
		}
	}
	if strings.Contains(out, "export KCM_EXPIRES_AT") || strings.Contains(out, "export KCM_FILE") {
		t.Fatal("rename changed timeout or source path")
	}
}

func TestRenameBackupFailurePreservesSource(t *testing.T) {
	s := fixture(t)
	path := filepath.Join(t.TempDir(), "source.yaml")
	writeConfig(t, path, "https://localhost", "old")
	original, _ := os.ReadFile(path)
	if err := os.MkdirAll(s.Root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Root, "archive"), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "rename", path, "--rename-context", "new", "--no-prompt"); err == nil {
		t.Fatal("ignored failed backup")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(original, after) {
		t.Fatal("edited source before backing it up")
	}
}
