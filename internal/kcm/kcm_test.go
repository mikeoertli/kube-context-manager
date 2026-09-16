package kcm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/tools/clientcmd"
	api "k8s.io/client-go/tools/clientcmd/api"
)

func fixture(t *testing.T) *Settings {
	t.Helper()
	base := t.TempDir()
	path := filepath.Join(base, "settings.toml")
	if e := os.WriteFile(path, []byte(fmt.Sprintf("kube_root = %q\n", filepath.Join(base, "kube"))), 0600); e != nil {
		t.Fatal(e)
	}
	s, e := loadSettings(path)
	if e != nil {
		t.Fatal(e)
	}
	t.Setenv("KCM_SETTINGS", path)
	t.Setenv("KCM_PROFILE", "local")
	t.Setenv("KCM_CONTEXT", "")
	t.Setenv("KCM_FILE", "")
	t.Setenv("KCM_EXPIRES_AT", "")
	t.Setenv("KCM_PREVIOUS_CONTEXT", "")
	t.Setenv("KCM_PREVIOUS_FILE", "")
	return s
}

func writeConfig(t *testing.T, path, server string, names ...string) *api.Config {
	t.Helper()
	c := api.NewConfig()
	c.Clusters["cluster"] = &api.Cluster{Server: server}
	c.AuthInfos["user"] = &api.AuthInfo{Token: "fixture-only"}
	for _, n := range names {
		c.Contexts[n] = &api.Context{Cluster: "cluster", AuthInfo: "user", Namespace: "original"}
	}
	c.CurrentContext = names[0]
	b, e := clientcmd.Write(*c)
	if e != nil {
		t.Fatal(e)
	}
	if e = atomicWrite(path, b, false); e != nil {
		t.Fatal(e)
	}
	return c
}

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	b := &bytes.Buffer{}
	c := newCommand("test", true)
	c.SetOut(b)
	c.SetErr(b)
	c.SetArgs(args)
	e := c.Execute()
	return b.String(), e
}

func TestLocalDetection(t *testing.T) {
	s := fixture(t)
	s.LocalHosts = []string{"minikube.test"}
	s.LocalCIDRs = []string{"192.168.49.2/32"}
	for _, tc := range []struct {
		url  string
		want bool
	}{{"https://127.0.0.1:6443", true}, {"https://127.20.1.2", true}, {"https://[::1]:6443", true}, {"https://LOCALHOST.", true}, {"https://minikube.test", true}, {"https://192.168.49.2", true}, {"https://192.168.49.3", false}, {"https://10.0.0.1", false}, {"https://localhost.evil.example", false}, {"https://localhost@prod.example", false}, {"file://localhost", false}} {
		t.Run(tc.url, func(t *testing.T) {
			if got := s.local(tc.url); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestSettingsOverlayAndTimeout(t *testing.T) {
	s := fixture(t)
	b := fmt.Sprintf("kube_root=%q\n[profiles.prod]\ntimeout='0'\n[profiles.support]\ndir='prod'\nemoji='S'\n", s.Root)
	if e := os.WriteFile(s.Path, []byte(b), 0600); e != nil {
		t.Fatal(e)
	}
	s, e := loadSettings(s.Path)
	if e != nil {
		t.Fatal(e)
	}
	if d, _ := s.dir("prod"); d != filepath.Join(s.Root, "prod") {
		t.Fatalf("lost default dir: %s", d)
	}
	if s.timeout("prod", filepath.Join(s.Root, "prod", "a")) != 0 {
		t.Fatal("disabled timeout ignored")
	}
	if s.timeout("support", filepath.Join(s.Root, "prod", "a")) != 8*time.Hour {
		t.Fatal("prod path default missing")
	}
	if s.timeout("local", filepath.Join(s.Root, "config")) != 0 {
		t.Fatal("local should not expire")
	}
}

func TestInvalidSettings(t *testing.T) {
	for _, body := range []string{"unknown=true", "[profiles.prod]\ntimeout='-1h'", "[profiles.prod]\ntimeout='tomorrow'", "[profiles.local]\ndir='prod'", "local_cidrs=['oops']"} {
		t.Run(body, func(t *testing.T) {
			s := fixture(t)
			os.WriteFile(s.Path, []byte(body), 0600)
			if _, e := loadSettings(s.Path); e == nil {
				t.Fatal("invalid settings accepted")
			}
		})
	}
}

func TestProfileIsolationAndWaiverBinding(t *testing.T) {
	s := fixture(t)
	local := filepath.Join(s.Root, "config")
	writeConfig(t, local, "https://localhost:6443", "local")
	writeConfig(t, filepath.Join(s.Root, "prod", "config"), "https://prod.example", "prod")
	cs, e := s.contexts("local")
	if e != nil || len(cs) != 1 || cs[0].Name != "local" {
		t.Fatalf("local leak: %+v %v", cs, e)
	}
	remote := filepath.Join(s.Root, "remote")
	writeConfig(t, remote, "https://customer.example", "customer")
	if _, e = s.contexts("prod"); e == nil {
		t.Fatal("unwaived root accepted")
	}
	if _, e = run(t, "waive", "customer"); e != nil {
		t.Fatal(e)
	}
	if e = s.validateRoot(); e != nil {
		t.Fatal(e)
	}
	c, _ := readConfig(remote)
	c.Clusters["cluster"].Server = "https://different.example"
	b, _ := clientcmd.Write(*c)
	os.WriteFile(remote, b, 0600)
	if e = s.validateRoot(); e == nil {
		t.Fatal("waiver survived endpoint change")
	}
	if _, e = run(t, "unwaive", "customer"); e != nil {
		t.Fatal(e)
	}
}

func TestDuplicateAndEscaping(t *testing.T) {
	s := fixture(t)
	name := "customer ' $(touch NEVER) `false`"
	a := filepath.Join(s.Root, "prod", "a")
	b := filepath.Join(s.Root, "prod", "b")
	writeConfig(t, a, "https://prod.example", name)
	writeConfig(t, b, "https://prod.example", name)
	t.Setenv("KCM_PROFILE", "prod")
	if _, e := run(t, "context", name); e == nil {
		t.Fatal("duplicate accepted")
	}
	out, e := run(t, "context", name, "--file", a)
	if e != nil {
		t.Fatal(e)
	}
	for _, shell := range []string{"/bin/bash", "/bin/zsh"} {
		if _, err := os.Stat(shell); os.IsNotExist(err) {
			continue
		}
		cmd := exec.Command(shell, "-c", out+"\nprintf '%s' \"$KCM_CONTEXT\"")
		got, e := cmd.Output()
		if e != nil || string(got) != name {
			t.Fatalf("quoting failed %s: %q %v", shell, got, e)
		}
	}
}

func TestSwitchPreservesSourceAndDeadline(t *testing.T) {
	s := fixture(t)
	path := filepath.Join(s.Root, "prod", "config")
	writeConfig(t, path, "https://prod.example", "one", "two")
	original, _ := os.ReadFile(path)
	t.Setenv("KCM_PROFILE", "prod")
	now := time.Unix(2000000000, 0)
	deadline := now.Add(time.Hour).Unix()
	t.Setenv("KCM_EXPIRES_AT", fmt.Sprint(deadline))
	var b bytes.Buffer
	if e := s.switchContext(&b, "two", "", now); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(b.String(), fmt.Sprintf("KCM_EXPIRES_AT='%d'", deadline)) {
		t.Fatal("switch extended expiry")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(original, after) {
		t.Fatal("context switch changed source")
	}
	t.Setenv("KCM_CONTEXT", "two")
	t.Setenv("KCM_FILE", path)
	if e := s.namespace("app", false, &bytes.Buffer{}); e != nil {
		t.Fatal(e)
	}
	c, e := readConfig(path)
	if e != nil {
		t.Fatal(e)
	}
	if c.Contexts["two"].Namespace != "app" || c.Contexts["one"].Namespace != "original" || c.CurrentContext != "one" {
		t.Fatalf("namespace changed wrong context: %+v", c)
	}
}

func TestImportRulesAndMove(t *testing.T) {
	s := fixture(t)
	source := filepath.Join(t.TempDir(), "download.yaml")
	writeConfig(t, source, "https://prod.example", "ugly")
	original, _ := os.ReadFile(source)
	if _, e := run(t, "install", source, "--no-prompt"); e == nil {
		t.Fatal("remote imported without profile")
	}
	if _, e := run(t, "install", source, "--no-prompt", "--profile", "local"); e == nil {
		t.Fatal("remote imported at root")
	}
	if _, e := run(t, "install", source, "--no-prompt", "--profile", "prod", "--rename-context", "readable", "--rename-cluster", "cluster-a", "--rename-user", "user-a", "--move"); e != nil {
		t.Fatal(e)
	}
	dest := filepath.Join(s.Root, "prod", "download.yaml")
	c, e := readConfig(dest)
	if e != nil {
		t.Fatal(e)
	}
	if c.CurrentContext != "readable" || c.Contexts["readable"].Cluster != "cluster-a" || c.Contexts["readable"].AuthInfo != "user-a" || c.Contexts["readable"].Namespace != "original" {
		t.Fatal("rename failed")
	}
	if _, e := os.Stat(source); !os.IsNotExist(e) {
		t.Fatal("source not moved")
	}
	backups, _ := filepath.Glob(filepath.Join(s.Root, "archive", "*"))
	if len(backups) != 1 {
		t.Fatal("missing backup")
	}
	backup, _ := os.ReadFile(backups[0])
	if !bytes.Equal(backup, original) {
		t.Fatal("backup is not original")
	}
	info, _ := os.Stat(dest)
	if info.Mode().Perm() != 0600 {
		t.Fatal("permissions not private")
	}
	writeConfig(t, source, "https://prod.example", "another")
	before, _ := os.ReadFile(dest)
	if _, e := run(t, "install", source, "--no-prompt", "--profile", "prod", "--move"); e == nil {
		t.Fatal("overwrite accepted")
	}
	after, _ := os.ReadFile(dest)
	if !bytes.Equal(before, after) {
		t.Fatal("destination overwritten")
	}
	if _, e := os.Stat(source); e != nil {
		t.Fatal("failed move deleted source")
	}
}

func TestImportMultiAndRelativeReferences(t *testing.T) {
	s := fixture(t)
	dir := t.TempDir()
	source := filepath.Join(dir, "config")
	c := writeConfig(t, source, "https://dev.example", "one", "two")
	os.WriteFile(filepath.Join(dir, "ca.pem"), []byte("fixture-ca"), 0600)
	c.Clusters["cluster"].CertificateAuthority = "ca.pem"
	c.AuthInfos["user"].TokenFile = "token.txt"
	c.AuthInfos["user"].Exec = &api.ExecConfig{Command: "./auth-helper", APIVersion: "client.authentication.k8s.io/v1", InteractiveMode: api.NeverExecInteractiveMode}
	b, _ := clientcmd.Write(*c)
	os.WriteFile(source, b, 0600)
	if _, e := run(t, "install", source, "--no-prompt", "--profile", "dev"); e == nil {
		t.Fatal("ambiguous source accepted")
	}
	if _, e := run(t, "install", source, "--no-prompt", "--profile", "dev", "--context", "one", "--move"); e == nil {
		t.Fatal("partial move accepted")
	}
	if _, e := run(t, "install", source, "--no-prompt", "--profile", "dev", "--all-contexts"); e != nil {
		t.Fatal(e)
	}
	dest, e := readConfig(filepath.Join(s.Root, "dev", "config"))
	if e != nil {
		t.Fatal(e)
	}
	if len(dest.Contexts) != 2 || string(dest.Clusters["cluster"].CertificateAuthorityData) != "fixture-ca" || dest.Clusters["cluster"].CertificateAuthority != "" {
		t.Fatal("relative CA lost")
	}
	u := dest.AuthInfos["user"]
	if u.TokenFile != filepath.Join(dir, "token.txt") || u.Exec.Command != filepath.Join(dir, "auth-helper") {
		t.Fatalf("relative auth references lost: %+v", u)
	}
}

func TestDryRunLocalAndSymlinks(t *testing.T) {
	s := fixture(t)
	source := filepath.Join(t.TempDir(), "local.anything")
	writeConfig(t, source, "https://[::1]:6443", "local")
	if _, e := run(t, "install", source, "--no-prompt", "--dry-run"); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(s.Root); !os.IsNotExist(e) {
		t.Fatal("dry run wrote destination")
	}
	if _, e := run(t, "install", source, "--no-prompt"); e != nil {
		t.Fatal(e)
	}
	cs, e := s.contexts("local")
	if e != nil || len(cs) != 1 {
		t.Fatalf("custom filename not discovered: %v", e)
	}
	os.MkdirAll(filepath.Join(s.Root, "prod"), 0700)
	if e := os.Symlink(source, filepath.Join(s.Root, "prod", "escape")); e != nil {
		t.Fatal(e)
	}
	if _, e := s.contexts("prod"); e == nil {
		t.Fatal("cross-profile symlink accepted")
	}
}

func TestShellResetAndWholeLine(t *testing.T) {
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh unavailable")
	}
	fixture(t)
	script := "_KCM_BIN=/usr/bin/true\n" + commonShell + "\nexport KCM_PROFILE=prod KUBECONFIG=/prod KCM_CONTEXT=remote KCM_EXPIRES_AT=1\n_kcm_expire\nprintf '%s|%s|%s|%s' \"$KCM_PROFILE\" \"$KUBECONFIG\" \"${KCM_CONTEXT:-}\" \"${KCM_EXPIRES_AT:-}\"\n"
	cmd := exec.Command("zsh", "-c", "zmodload zsh/datetime\n"+script)
	got, e := cmd.Output()
	if e != nil {
		t.Fatal(e)
	}
	if string(got) != "local|/dev/null||" {
		t.Fatalf("bad reset: %s", got)
	}
}

func TestWaiverFileFormat(t *testing.T) {
	s := fixture(t)
	writeConfig(t, filepath.Join(s.Root, "config"), "https://remote.example", "remote")
	if _, e := run(t, "waive", "remote"); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(s.Root, ".kcm_waivers"))
	if e != nil {
		t.Fatal(e)
	}
	var w []Waiver
	if e = json.Unmarshal(b, &w); e != nil || len(w) != 1 || w[0].Server != "https://remote.example" {
		t.Fatal("invalid waiver")
	}
}
