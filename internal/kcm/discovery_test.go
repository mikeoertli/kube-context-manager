package kcm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoveryIgnoresAncillaryFiles(t *testing.T) {
	s := fixture(t)
	t.Setenv("KUBECONFIG", "/dev/null")
	writeConfig(t, filepath.Join(s.Root, "local.anything"), "https://localhost:6443", "local")
	writeConfig(t, filepath.Join(s.Root, "dev", "remote.bundle"), "https://dev.invalid", "remote")
	files := map[string]string{
		"README.md":      "# Kubernetes\n```yaml\nkind: Config\ncontexts: [\n```\n",
		"guide.MARKDOWN": "kind: Config\ncontexts: [",
		"notes.rst":      "kind: Config\ncontexts: [",
		"README":         "kind: Config\ncontexts: [",
		"LICENSE":        "Some license text",
		"NOTICE":         "Not a config",
		"notes.txt":      "Things to remember: [unfinished note",
		"manifest.yaml":  "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: example\ndata:\n  contexts: not-a-kubeconfig\n",
		"settings.json":  `{"theme":"dark","editor":{"contexts":[]}}`,
		"script.sh":      "#!/bin/sh\necho hello\n",
		"empty":          "",
		"binary":         "\x00\xff\xfe",
	}
	for _, dir := range []string{s.Root, filepath.Join(s.Root, "dev")} {
		for name, body := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, profile := range []string{"local", "dev"} {
		contexts, err := s.contexts(profile)
		if err != nil || len(contexts) != 1 {
			t.Fatalf("%s: got %+v, %v", profile, contexts, err)
		}
	}
	for _, args := range [][]string{{"doctor", "--quiet"}, {"list"}, {"contexts"}, {"context", "local"}} {
		if _, err := run(t, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
}

func TestDiscoveryRejectsRecognizableBrokenConfigs(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"config", "not a kubeconfig"},
		{"kubeconfig", ""},
		{"customer.kubeconfig", "not a kubeconfig"},
		{"kubeconfig.customer", "not a kubeconfig"},
		{"broken.yaml", "apiVersion: v1\nkind: Config\ncontexts: ["},
		{"broken.json", `{"kind":"Config","contexts":[`},
		{"partial", "contexts: ["},
		{"empty.yaml", "apiVersion: v1\nkind: Config\ncontexts: []"},
		{"missing-ref.yaml", "apiVersion: v1\nkind: Config\ncontexts:\n- name: broken\n  context:\n    cluster: missing\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, tc.name), []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := scanDir(dir); err == nil {
				t.Fatal("recognizable broken kubeconfig was silently ignored")
			}
		})
	}
}

func TestDiscoveryStillValidatesRootAndExplicitImports(t *testing.T) {
	s := fixture(t)
	writeConfig(t, filepath.Join(s.Root, "remote.anything"), "https://remote.invalid", "remote")
	if err := os.WriteFile(filepath.Join(s.Root, "README.md"), []byte("# README"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.validateRoot(); err == nil || !strings.Contains(err.Error(), "non-local") {
		t.Fatalf("root check was bypassed: %v", err)
	}
	source := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(source, []byte("Just notes"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfig(source); err == nil {
		t.Fatal("explicit read was not strict")
	}
	if _, err := run(t, "install", source, "--profile", "dev", "--no-prompt"); err == nil {
		t.Fatal("explicit import accepted unrelated text")
	}
}
