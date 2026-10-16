package kcm

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpiryLocalSelection(t *testing.T) {
	for _, tc := range []struct {
		name               string
		files              map[string][]string
		disabled           bool
		wantName, wantFile string
	}{
		{name: "only context", files: map[string][]string{"cluster.yaml": {"local-lab"}}, wantName: "local-lab", wantFile: "cluster.yaml"},
		{name: "file named config", files: map[string][]string{"config": {"beta", "zeta"}, "other.yaml": {"aaa", "config"}}, wantName: "beta", wantFile: "config"},
		{name: "first context", files: map[string][]string{"b.yaml": {"beta"}, "z.yaml": {"alpha"}}, wantName: "alpha", wantFile: "z.yaml"},
		{name: "duplicate names", files: map[string][]string{"b.yaml": {"config"}, "a.yaml": {"config"}}, wantName: "config", wantFile: "a.yaml"},
		{name: "no contexts"},
		{name: "disabled", files: map[string][]string{"config": {"local-lab"}}, disabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := fixture(t)
			if !s.AutoRevertLocal {
				t.Fatal("auto revert should default to enabled")
			}
			for file, names := range tc.files {
				writeConfig(t, filepath.Join(s.Root, file), "https://127.0.0.1:6443", names...)
			}
			s.AutoRevertLocal = !tc.disabled
			var out bytes.Buffer
			if err := s.expireToLocal(&out); err != nil {
				t.Fatal(err)
			}
			// Evaluate real shell assignments to check cleanup and source selection.
			wantFile := "/dev/null"
			if tc.wantFile != "" {
				wantFile = filepath.Join(s.Root, tc.wantFile)
			}
			script := "export KCM_PROFILE=prod KCM_CONTEXT=old KCM_FILE=/old KUBECONFIG=/old KCM_EXPIRES_AT=1 KCM_PREVIOUS_CONTEXT=remote KCM_PREVIOUS_FILE=/remote\n" + out.String() +
				"printf '%s|%s|%s|%s|%s|%s' \"$KCM_PROFILE\" \"${KCM_CONTEXT:-}\" \"$KUBECONFIG\" \"${KCM_EXPIRES_AT:-}\" \"${KCM_PREVIOUS_CONTEXT:-}\" \"${KCM_PREVIOUS_FILE:-}\""
			got, err := exec.Command("/bin/sh", "-c", script).CombinedOutput()
			if want := "local|" + tc.wantName + "|" + wantFile + "|||"; err != nil || string(got) != want {
				t.Fatalf("got %q, want %q: %v", got, want, err)
			}
		})
	}
}

func TestExpiryConfigAndDiscoveryFailures(t *testing.T) {
	s := fixture(t)
	if err := os.WriteFile(s.Path, []byte(fmt.Sprintf("kube_root = %q\nauto_revert_local = false\n", s.Root)), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadSettings(s.Path)
	if err != nil || loaded.AutoRevertLocal {
		t.Fatalf("false ignored: %+v %v", loaded, err)
	}
	writeConfig(t, filepath.Join(s.Root, "config"), "https://remote.example", "config")
	var out bytes.Buffer
	if err := s.expireToLocal(&out); err == nil || out.Len() != 0 {
		t.Fatalf("invalid root produced assignments: %q %v", out.String(), err)
	}
	// Even waived remote contexts must not become an automatic local target.
	if _, err := run(t, "waive", "config"); err != nil {
		t.Fatal(err)
	}
	if err := s.expireToLocal(&out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "export KCM_CONTEXT=") {
		t.Fatalf("selected a remote root entry: %s", out.String())
	}
	// A malformed file is also ignored when automatic selection is disabled.
	if err := os.WriteFile(filepath.Join(s.Root, "config"), []byte("invalid kubeconfig"), 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	loaded.AutoRevertLocal = false
	if err := loaded.expireToLocal(&out); err != nil {
		t.Fatal(err)
	}
}

func TestExpiryKeepsSymlinkPathAndSource(t *testing.T) {
	s := fixture(t)
	source := filepath.Join(t.TempDir(), "source.yaml")
	writeConfig(t, source, "https://localhost:6443", "first", "second")
	link := filepath.Join(s.Root, "config")
	if err := os.MkdirAll(s.Root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(source)
	out, err := run(t, "__expire")
	if err != nil || !strings.Contains(out, "export KUBECONFIG="+quote(link)) {
		t.Fatalf("lost symlink path: %q %v", out, err)
	}
	after, _ := os.ReadFile(source)
	if !bytes.Equal(before, after) {
		t.Fatal("expiry changed source kubeconfig")
	}
}
