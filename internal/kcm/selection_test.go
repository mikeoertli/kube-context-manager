package kcm

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileShorthand(t *testing.T) {
	s := fixture(t)
	path := filepath.Join(s.Root, "dev", "config")
	writeConfig(t, path, "https://dev.invalid", "api-dev")
	t.Setenv("KCM_PROFILE", "prod")
	t.Setenv("KCM_EXPIRES_AT", "1")
	for _, args := range [][]string{
		{"missing", "--context", "api-dev"}, {"dev", "--context", "missing"},
		{"dev"}, {"dev", "--context", ""}, {"--context", "api-dev"}, {"dev", "extra", "--context", "api-dev"},
	} {
		out, err := run(t, args...)
		if err == nil || out != "" {
			t.Fatalf("invalid selection %v: %q %v", args, out, err)
		}
	}
	out, err := run(t, "dev", "--context", "api-dev")
	if err != nil {
		t.Fatal(err)
	}
	script := "export KCM_PREVIOUS_CONTEXT=old KCM_PREVIOUS_FILE=/old\n" + out + "printf '%s|%s|%s|%s|%s|%s' \"$KCM_PROFILE\" \"$KCM_CONTEXT\" \"$KUBECONFIG\" \"${KCM_EXPIRES_AT:-}\" \"${KCM_PREVIOUS_CONTEXT:-}\" \"${KCM_PREVIOUS_FILE:-}\""
	got, err := exec.Command("/bin/sh", "-c", script).CombinedOutput()
	if err != nil || string(got) != "dev|api-dev|"+path+"|||" {
		t.Fatalf("selection: %s %v", got, err)
	}
	writeConfig(t, filepath.Join(s.Root, "dev", "duplicate"), "https://dev.invalid", "api-dev")
	if out, err := run(t, "dev", "--context", "api-dev"); err == nil || !strings.Contains(err.Error(), "ambiguous") || out != "" {
		t.Fatalf("ambiguous: %s %v", out, err)
	}
	if _, err := run(t, "dev", "--context=api-dev", "--file", path); err != nil {
		t.Fatal(err)
	}
	cmd := NewCommand("test")
	cmd.SetArgs([]string{"dev", "--context", "api-dev"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "shell integration") {
		t.Fatalf("shell requirement: %v", err)
	}
}

func TestResetLocalSelection(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		files                 map[string][]string
		wantFile, wantContext string
	}{
		{"single", map[string][]string{"lab.yaml": {"lab"}}, "lab.yaml", "lab"},
		{"config preferred", map[string][]string{"config": {"beta", "zeta"}, "aaa.yaml": {"alpha"}}, "config", "beta"},
		{"stable fallback", map[string][]string{"z.yaml": {"alpha"}, "a.yaml": {"beta"}}, "z.yaml", "alpha"},
		{"empty", nil, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := fixture(t)
			for file, names := range tc.files {
				writeConfig(t, filepath.Join(s.Root, file), "https://localhost:6443", names...)
			}
			// Explicit reset is independent of expiry's auto-revert preference.
			content, _ := os.ReadFile(s.Path)
			if err := os.WriteFile(s.Path, append(content, []byte("\nauto_revert_local = false\n")...), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("KCM_PROFILE", "prod")
			t.Setenv("KCM_CONTEXT", "remote")
			t.Setenv("KCM_EXPIRES_AT", "123")
			out, err := run(t, "reset")
			if tc.wantContext == "" {
				if err == nil || out != "" {
					t.Fatalf("empty reset: %q %v", out, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := exec.Command("/bin/sh", "-c", out+"printf '%s|%s|%s|%s' \"$KCM_PROFILE\" \"$KCM_CONTEXT\" \"$KUBECONFIG\" \"${KCM_EXPIRES_AT:-}\"").CombinedOutput()
			if err != nil || string(got) != "local|"+tc.wantContext+"|"+filepath.Join(s.Root, tc.wantFile)+"|" {
				t.Fatalf("reset: %s %v", got, err)
			}
		})
	}
	s := fixture(t)
	writeConfig(t, filepath.Join(s.Root, "config"), "https://remote.invalid", "remote")
	run(t, "waive", "remote")
	if out, err := run(t, "reset"); err == nil || out != "" {
		t.Fatalf("reset selected waived remote: %s %v", out, err)
	}
	cmd := NewCommand("test")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"reset"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("reset accepted without shell integration")
	}
}
