package kcm

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/client-go/tools/clientcmd"
	api "k8s.io/client-go/tools/clientcmd/api"
)

func stubClipboard(t *testing.T, content []byte) string {
	t.Helper()
	fixture := filepath.Join(t.TempDir(), "clipboard")
	if err := os.WriteFile(fixture, content, 0600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	t.Setenv("KCM_TEST_CLIPBOARD", fixture)
	for _, name := range []string{"pbpaste", "wl-paste", "xclip", "xsel"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n/bin/cat \"$KCM_TEST_CLIPBOARD\"\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return fixture
}

func TestClipboardImport(t *testing.T) {
	s := fixture(t)
	dir := t.TempDir()
	source := filepath.Join(dir, "fixture.yaml")
	config := writeConfig(t, source, "https://clipboard.invalid", "opaque")
	config.Clusters["cluster"].CertificateAuthority = "ca.crt"
	config.AuthInfos["user"].TokenFile = "token"
	config.AuthInfos["user"].Exec = &api.ExecConfig{Command: "./credential-helper", APIVersion: "client.authentication.k8s.io/v1", InteractiveMode: api.NeverExecInteractiveMode}
	content, err := clientcmd.Write(*config)
	if err != nil {
		t.Fatal(err)
	}
	clipboard := stubClipboard(t, content)
	staging := t.TempDir()
	t.Setenv("TMPDIR", staging)
	t.Chdir(dir)
	if err := os.WriteFile("ca.crt", []byte("certificate-data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("token", []byte("fixture-token"), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"import", "--clipboard", "--profile", "dev", "--name", "api-dev", "--readable", "--no-prompt"}
	if _, err := run(t, append(args, "--dry-run")...); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.Root); !os.IsNotExist(err) {
		t.Fatal("clipboard dry-run wrote profile files")
	}
	out, err := run(t, args...)
	if err != nil {
		t.Fatal(err)
	}
	got, err := readConfig(filepath.Join(s.Root, "dev", "api-dev"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Contexts["api-dev"] == nil || !bytes.Equal(got.Clusters["api-dev"].CertificateAuthorityData, []byte("certificate-data")) || got.AuthInfos["api-dev-user"].TokenFile != filepath.Join(dir, "token") || got.AuthInfos["api-dev-user"].Exec.Command != filepath.Join(dir, "credential-helper") {
		t.Fatal("clipboard names or relative credential paths were not preserved")
	}
	after, _ := os.ReadFile(clipboard)
	if !bytes.Equal(after, content) {
		t.Fatal("clipboard import cleared or changed the clipboard")
	}
	if !strings.Contains(out, "Source: clipboard") || strings.Contains(out, "kcm-clipboard-") {
		t.Fatalf("temporary path leaked into summary: %s", out)
	}
	backups, _ := filepath.Glob(filepath.Join(s.Root, "archive", "*"))
	if len(backups) != 0 {
		t.Fatal("clipboard import treated staging file as an original source")
	}
	entries, _ := os.ReadDir(staging)
	if len(entries) != 0 {
		t.Fatal("clipboard import retained staging files")
	}
}

func TestClipboardValidationAndCleanup(t *testing.T) {
	s := fixture(t)
	clipboard := stubClipboard(t, []byte("not a kubeconfig"))
	staging := t.TempDir()
	t.Setenv("TMPDIR", staging)
	if _, err := run(t, "import", "--clipboard", "--profile", "dev", "--name", "bad", "--no-prompt"); err == nil {
		t.Fatal("accepted invalid clipboard content")
	}
	entries, _ := os.ReadDir(staging)
	if len(entries) != 0 {
		t.Fatal("failed import left clipboard staging files")
	}
	if _, err := os.Stat(s.Root); !os.IsNotExist(err) {
		t.Fatal("invalid clipboard import created profile files")
	}
	if err := os.WriteFile(clipboard, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "import", "--clipboard", "--profile", "dev", "--no-prompt"); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty clipboard: %v", err)
	}
	for _, args := range [][]string{
		{"import", "--clipboard", "--file", "missing"}, {"import", "--clipboard", "--dir", "missing"},
		{"import", "--clipboard", "--select"}, {"import", "path", "--clipboard"},
	} {
		if _, err := run(t, args...); err == nil {
			t.Fatalf("accepted conflicting source: %v", args)
		}
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := readClipboard(); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("missing reader: %v", err)
	}
}

func TestClipboardStagingPermissions(t *testing.T) {
	stubClipboard(t, []byte("fixture"))
	path, cleanup, err := clipboardSource()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("staging permissions: %v %v", info, err)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil || dirInfo.Mode().Perm() != 0700 {
		t.Fatalf("staging directory permissions: %v %v", dirInfo, err)
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("staging cleanup failed")
	}
}
