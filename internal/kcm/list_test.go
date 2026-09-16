package kcm

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"k8s.io/client-go/tools/clientcmd"
)

func TestListGroupedSelections(t *testing.T) {
	s := fixture(t)
	local := filepath.Join(s.Root, "config")
	prod := filepath.Join(s.Root, "prod", "bundle")
	duplicate := filepath.Join(s.Root, "dev", "config")
	custom := filepath.Join(t.TempDir(), "custom-config")
	writeConfig(t, local, "https://localhost:6443", "local-context")
	writeConfig(t, prod, "https://prod.invalid", "global-context", "shell-context")
	writeConfig(t, duplicate, "https://dev.invalid", "global-context")
	writeConfig(t, custom, "https://custom.invalid", "custom-context")
	settings, _ := os.ReadFile(s.Path)
	settings = append(settings, []byte("\n[profiles.support]\ndir = "+strconv.Quote(filepath.Dir(custom))+"\nemoji = 'S'\n")...)
	if err := os.WriteFile(s.Path, settings, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", prod)
	t.Setenv("KCM_PROFILE", "prod")
	t.Setenv("KCM_CONTEXT", "shell-context")
	t.Setenv("KCM_FILE", prod)
	before, _ := os.ReadFile(prod)
	env := os.Environ()
	out, err := run(t, "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"local", "dev", "prod [current profile]", "qa", "other", "support", "(no contexts)", "custom-context", custom, prod, "original", "https://prod.invalid"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	var globalRows, shellRows int
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		if fields[0] == "*" && fields[1] != "=" {
			globalRows++
			if fields[1] != "global-context" || fields[len(fields)-1] != prod {
				t.Errorf("wrong global marker: %s", line)
			}
		}
		if fields[0] == ">" {
			shellRows++
			if fields[1] != "shell-context" {
				t.Errorf("wrong shell marker: %s", line)
			}
		}
	}
	if globalRows != 1 || shellRows != 1 {
		t.Fatalf("wrong marker counts %d/%d:\n%s", globalRows, shellRows, out)
	}
	after, _ := os.ReadFile(prod)
	if !bytes.Equal(before, after) || !reflect.DeepEqual(env, os.Environ()) {
		t.Fatal("listing changed config or selection")
	}
	t.Setenv("KCM_CONTEXT", "global-context")
	out, err = run(t, "list")
	if err != nil || !strings.Contains(out, "*>  global-context") {
		t.Fatalf("combined markers: %v\n%s", err, out)
	}
}

func TestGlobalContextMergeAndFallback(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	first := filepath.Join(t.TempDir(), "first")
	second := filepath.Join(t.TempDir(), "second")
	writeConfig(t, first, "https://first.invalid", "duplicate")
	writeConfig(t, second, "https://second.invalid", "duplicate", "second")
	for _, tc := range []struct{ name, env, wantName, wantFile string }{
		{"first wins", first + string(os.PathListSeparator) + second, "duplicate", first},
		{"reverse order", second + string(os.PathListSeparator) + first, "duplicate", second},
		{"empty selection", "/dev/null", "", ""},
		{"missing file", filepath.Join(t.TempDir(), "missing"), "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("KUBECONFIG", tc.env)
			name, file, err := globalContext()
			if err != nil || name != tc.wantName || file != tc.wantFile {
				t.Fatalf("got %q %q %v", name, file, err)
			}
		})
	}
	// current-context may originate in a different file from its context stanza.
	config, err := readConfig(first)
	if err != nil {
		t.Fatal(err)
	}
	config.CurrentContext = "second"
	b, err := clientcmd.Write(*config)
	if err != nil {
		t.Fatal(err)
	}
	if err = atomicWrite(first, b, true); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", first+string(os.PathListSeparator)+second)
	name, file, err := globalContext()
	if err != nil || name != "second" || file != second {
		t.Fatalf("merged origin: %q %q %v", name, file, err)
	}
	defaultFile := filepath.Join(os.Getenv("HOME"), ".kube", "config")
	writeConfig(t, defaultFile, "https://localhost", "default-context")
	t.Setenv("KUBECONFIG", "")
	name, file, err = globalContext()
	if err != nil || name != "default-context" || file != defaultFile {
		t.Fatalf("fallback: %q %q %v", name, file, err)
	}
}

func TestListNoGlobalAndBrokenGlobal(t *testing.T) {
	s := fixture(t)
	local := filepath.Join(s.Root, "config")
	writeConfig(t, local, "https://localhost", "local-context")
	t.Setenv("KUBECONFIG", "/dev/null")
	out, err := run(t, "list")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "*  local-context") || strings.Contains(out, ">  local-context") {
		t.Fatalf("invented selection:\n%s", out)
	}
	// Invalid external global configuration should not hide the discovered inventory.
	broken := filepath.Join(t.TempDir(), "broken")
	if err = os.WriteFile(broken, []byte("invalid: ["), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", broken)
	out, err = run(t, "list")
	if err != nil || !strings.Contains(out, "global selection unavailable") || !strings.Contains(out, "local-context") {
		t.Fatalf("got %v\n%s", err, out)
	}
}

func TestListValidationAndDefaultNamespace(t *testing.T) {
	s := fixture(t)
	t.Setenv("KUBECONFIG", "/dev/null")
	path := filepath.Join(s.Root, "config")
	writeConfig(t, path, "https://remote.invalid", "misplaced")
	if _, err := run(t, "list"); err == nil || !strings.Contains(err.Error(), "non-local") {
		t.Fatalf("missing root validation: %v", err)
	}
	if _, err := run(t, "waive", "misplaced"); err != nil {
		t.Fatal(err)
	}
	c, err := readConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	c.Contexts["misplaced"].Namespace = ""
	b, err := clientcmd.Write(*c)
	if err != nil {
		t.Fatal(err)
	}
	if err = atomicWrite(path, b, true); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "list")
	if err != nil || !strings.Contains(out, "default") {
		t.Fatalf("waived/default: %v %s", err, out)
	}
	bad := filepath.Join(s.Root, "qa", "broken")
	if err = os.MkdirAll(filepath.Dir(bad), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(bad, []byte("kind: Config\ncontexts: ["), 0600); err != nil {
		t.Fatal(err)
	}
	out, err = run(t, "list")
	if err == nil || !strings.Contains(err.Error(), "profile qa") || out != "" {
		t.Fatalf("malformed profile: %v %s", err, out)
	}
}
