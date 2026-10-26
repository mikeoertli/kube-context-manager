package kcm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHistoryNavigation(t *testing.T) {
	s := fixture(t)
	files := map[string]string{}
	for _, profile := range []string{"dev", "qa", "other"} {
		files[profile] = filepath.Join(s.Root, profile, "config")
		writeConfig(t, files[profile], "https://"+profile+".invalid", "same")
	}
	selectArgs := func(args ...string) {
		t.Helper()
		out, err := run(t, args...)
		if err != nil {
			t.Fatal(err)
		}
		applySelection(t, out)
	}
	selectArgs("dev", "--context", "same")
	selectArgs("qa", "--context", "same")
	selectArgs("other", "--context", "same")
	entries := recentHistory()
	if len(entries) != 2 || entries[0].Profile != "qa" || entries[1].Profile != "dev" {
		t.Fatalf("order: %+v", entries)
	}
	before := os.Getenv("KCM_HISTORY")
	cmd := NewCommand("test")
	var list bytes.Buffer
	cmd.SetOut(&list)
	cmd.SetArgs([]string{"history", "--list"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list.String(), files["qa"]) || !strings.Contains(list.String(), files["dev"]) || strings.Contains(list.String(), files["other"]) {
		t.Fatalf("list: %s", list.String())
	}
	if before != os.Getenv("KCM_HISTORY") || currentProfile() != "other" {
		t.Fatal("list changed selection/history")
	}
	// Older integrations may route this read-only command through __shell.
	// Its table must never enter the channel evaluated as shell statements.
	var exports, messages bytes.Buffer
	cmd = newCommand("test", true)
	cmd.SetOut(&exports)
	cmd.SetErr(&messages)
	cmd.SetArgs([]string{"history", "--list"})
	if err := cmd.Execute(); err != nil || exports.Len() != 0 || !strings.Contains(messages.String(), files["qa"]) {
		t.Fatalf("unsafe list output: stdout=%q stderr=%q error=%v", exports.String(), messages.String(), err)
	}
	for _, args := range [][]string{{"history", "0"}, {"history", "3"}, {"history", "bad"}, {"history", "1", "--list"}} {
		if out, err := run(t, args...); err == nil || out != "" {
			t.Fatalf("invalid history request %v: %q %v", args, out, err)
		}
	}
	selectArgs("history", "2")
	if currentProfile() != "dev" || os.Getenv("KCM_FILE") != files["dev"] {
		t.Fatal("number selection chose wrong profile/file")
	}
	entries = recentHistory()
	if len(entries) != 2 || entries[0].Profile != "other" || entries[1].Profile != "qa" {
		t.Fatalf("selected entry not moved out of history: %+v", entries)
	}
	selectArgs("-")
	if currentProfile() != "other" {
		t.Fatal("toggle did not use newest history entry")
	}
	// Stale entries remain visible, but are never activated without validation.
	if err := os.Remove(files["dev"]); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, "history", "1"); err == nil || out != "" {
		t.Fatalf("selected removed config: %s %v", out, err)
	}
	if currentProfile() != "other" {
		t.Fatal("failed history selection changed profile")
	}
	selectArgs("clear")
	if len(recentHistory()) != 0 || os.Getenv("KCM_HISTORY") != "" {
		t.Fatal("clear retained history")
	}
	if out, err := run(t, "history", "--list"); err != nil || !strings.Contains(out, "No previous") {
		t.Fatalf("empty list: %s %v", out, err)
	}
}

func TestHistoryPickerAndCancellation(t *testing.T) {
	s := fixture(t)
	path := filepath.Join(s.Root, "dev", "config")
	writeConfig(t, path, "https://dev.invalid", "first", "second", "third")
	for _, name := range []string{"first", "second", "third"} {
		out, err := run(t, "dev", "--context", name)
		if err != nil {
			t.Fatal(err)
		}
		applySelection(t, out)
	}
	bin := t.TempDir()
	stub := filepath.Join(bin, "fzf")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\ncase \"$*\" in *--no-sort*) ;; *) exit 2 ;; esac\nsed -n '2p'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := run(t, "history")
	if err != nil || !strings.Contains(out, "export KCM_CONTEXT='first'") {
		t.Fatalf("picker: %s %v", out, err)
	}
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 130\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, "history"); err == nil || out != "" {
		t.Fatalf("cancelled picker emitted selection: %s %v", out, err)
	}
}

func TestHistoryBoundDeduplicationAndRename(t *testing.T) {
	fixture(t)
	var entries []historyEntry
	for i := 0; i < historyLimit+5; i++ {
		entry := historyEntry{"dev", fmt.Sprintf("context-%d", i), fmt.Sprintf("/fixture/%d", i)}
		entries = append(entries, entry, entry)
	}
	content, _ := json.Marshal(entries)
	t.Setenv("KCM_HISTORY", string(content))
	got := recentHistory()
	if len(got) != historyLimit || got[0].Name != "context-0" || got[historyLimit-1].Name != fmt.Sprintf("context-%d", historyLimit-1) {
		t.Fatalf("history bound/dedup: %+v", got)
	}
	// A malformed environment value can fall back to the previous toggle format.
	t.Setenv("KCM_HISTORY", "invalid")
	t.Setenv("KCM_PREVIOUS_PROFILE", "qa")
	t.Setenv("KCM_PREVIOUS_CONTEXT", "previous")
	t.Setenv("KCM_PREVIOUS_FILE", "/fixture/previous")
	if got = recentHistory(); len(got) != 1 || got[0].Profile != "qa" {
		t.Fatalf("legacy history: %+v", got)
	}
	s := fixture(t)
	path := filepath.Join(s.Root, "dev", "config")
	writeConfig(t, path, "https://dev.invalid", "opaque", "other")
	for _, name := range []string{"opaque", "other"} {
		out, err := run(t, "dev", "--context", name)
		if err != nil {
			t.Fatal(err)
		}
		applySelection(t, out)
	}
	// Renaming a previously used context refreshes history only after success.
	var exports bytes.Buffer
	cmd := newCommand("test", true)
	cmd.SetOut(&exports)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"rename", path, "--context", "opaque", "--rename-context", "readable", "--no-prompt"})
	err := cmd.Execute()
	if err != nil {
		t.Fatal(err)
	}
	applySelection(t, exports.String())
	if got = recentHistory(); len(got) != 1 || got[0].Name != "readable" || os.Getenv("KCM_PREVIOUS_CONTEXT") != "readable" {
		t.Fatalf("rename history: %+v", got)
	}
	out, err := run(t, "history", "1")
	if err != nil || !strings.Contains(out, "export KCM_CONTEXT='readable'") {
		t.Fatalf("renamed history not selectable: %s %v", out, err)
	}
}
