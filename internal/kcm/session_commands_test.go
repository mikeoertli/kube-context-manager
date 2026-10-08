package kcm

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func selectedFixture(t *testing.T, profile string) (*Settings, string) {
	t.Helper()
	s := fixture(t)
	dir, err := s.dir(profile)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "config")
	server := "https://127.0.0.1:6443"
	if profile != "local" {
		server = "https://cluster.example"
	}
	writeConfig(t, file, server, "selected")
	t.Setenv("KCM_PROFILE", profile)
	t.Setenv("KCM_CONTEXT", "selected")
	t.Setenv("KCM_FILE", file)
	t.Setenv("KUBECONFIG", "/dev/null") // Inspect the selected source, not this sentinel.
	return s, file
}

func exportedExpiry(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "export KCM_EXPIRES_AT=") {
			return strings.Trim(strings.TrimPrefix(line, "export KCM_EXPIRES_AT="), "'")
		}
	}
	t.Fatalf("no expiry in %q", out)
	return ""
}

func TestRenewDurations(t *testing.T) {
	s, file := selectedFixture(t, "prod")
	beforeSettings, _ := os.ReadFile(s.Path)
	beforeConfig, _ := os.ReadFile(file)
	old := time.Now().Add(2 * time.Hour).Unix()
	t.Setenv("KCM_EXPIRES_AT", fmt.Sprint(old))
	out, err := run(t, "renew", "--extend", "24h")
	if err != nil || exportedExpiry(t, out) != fmt.Sprint(old+86400) {
		t.Fatalf("extend: %q %v", out, err)
	}
	for _, tc := range []struct {
		args     []string
		duration time.Duration
	}{
		{[]string{"renew", "--for", "96h"}, 96 * time.Hour},
		{[]string{"renew"}, 8 * time.Hour},
	} {
		start := time.Now().Add(tc.duration).Unix()
		out, err = run(t, tc.args...)
		if err != nil {
			t.Fatal(err)
		}
		deadline, err := strconv.ParseInt(exportedExpiry(t, out), 10, 64)
		if err != nil || deadline < start || deadline > time.Now().Add(tc.duration).Unix() {
			t.Fatalf("%v: %q %v", tc.args, out, err)
		}
	}
	afterSettings, _ := os.ReadFile(s.Path)
	afterConfig, _ := os.ReadFile(file)
	if string(beforeSettings) != string(afterSettings) || string(beforeConfig) != string(afterConfig) {
		t.Fatal("renew modified a shared file")
	}
}

func TestRenewValidationAndUntimedContext(t *testing.T) {
	selectedFixture(t, "dev")
	for _, deadline := range []string{"", "invalid", "1"} {
		t.Setenv("KCM_EXPIRES_AT", deadline)
		out, err := run(t, "renew", "--extend", "24h")
		if err == nil || out != "" {
			t.Fatalf("invalid deadline %q: %q %v", deadline, out, err)
		}
	}
	for _, args := range [][]string{
		{"renew", "--for", "0"}, {"renew", "--for", "-1h"}, {"renew", "--extend", "500ms"},
		{"renew", "--for", "tomorrow"}, {"renew", "--for", "1h", "--extend", "1h"},
	} {
		out, err := run(t, args...)
		if err == nil || strings.Contains(out, "export ") {
			t.Fatalf("accepted %v: %q %v", args, out, err)
		}
	}
	out, err := run(t, "renew", "--for", "24h")
	if err != nil || exportedExpiry(t, out) == "" {
		t.Fatalf("untimed --for: %q %v", out, err)
	}
	out, err = run(t, "renew")
	if err != nil || exportedExpiry(t, out) != "" {
		t.Fatalf("restore untimed default: %q %v", out, err)
	}
}

func TestPrintSelectionAndStatus(t *testing.T) {
	_, file := selectedFixture(t, "local")
	for _, tc := range []struct{ command, want string }{
		{"print-kubeconfig", file}, {"kc", file}, {"print-context-name", "selected"}, {"ctx", "selected"},
	} {
		out, err := run(t, tc.command)
		if err != nil || out != tc.want+"\n" {
			t.Fatalf("%s: %q %v", tc.command, out, err)
		}
	}
	out, err := run(t, "status")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"KCM session", file, "selected", "original", "No timeout", "other shells using this context"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q: %q", want, out)
		}
	}
	if strings.Contains(out, "/dev/null") || strings.Contains(out, "(shared)") || strings.Contains(out, "\x1b[") {
		t.Fatalf("unclear redirected status: %q", out)
	}
	t.Setenv("KCM_CONTEXT", "")
	t.Setenv("KCM_FILE", "")
	for _, command := range []string{"kc", "ctx", "renew", "renew --for 24h"} {
		out, err = run(t, strings.Fields(command)...)
		if err == nil || out != "" {
			t.Fatalf("unselected %s: %q %v", command, out, err)
		}
	}
	out, err = run(t, "status")
	if err != nil || !strings.Contains(out, "Not selected") || strings.Contains(out, "/dev/null") || strings.Contains(out, "Namespace changes") {
		t.Fatalf("unselected status: %q %v", out, err)
	}
}
