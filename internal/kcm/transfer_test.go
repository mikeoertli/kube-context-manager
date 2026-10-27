package kcm

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func stubTransferClipboard(t *testing.T) string {
	t.Helper()
	clipboard := stubClipboard(t, nil)
	// All readers and writers are fakes; tests never access the real clipboard.
	bin := t.TempDir()
	for _, name := range []string{"pbcopy", "wl-copy", "xclip", "xsel"} {
		body := "#!/bin/sh\n/bin/cat > \"$KCM_TEST_CLIPBOARD\"\n"
		if name == "xclip" || name == "xsel" {
			body = "#!/bin/sh\ncase \" $* \" in *' -in '*|*' --input '*) /bin/cat > \"$KCM_TEST_CLIPBOARD\" ;; *) /bin/cat \"$KCM_TEST_CLIPBOARD\" ;; esac\n"
		}
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return clipboard
}

func transferRun(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var out, messages bytes.Buffer
	cmd := newCommand("test", true)
	cmd.SetOut(&out)
	cmd.SetErr(&messages)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), messages.String(), err
}

func TestCopyPasteSelection(t *testing.T) {
	source := fixture(t)
	file := filepath.Join(source.Root, "prod", "shared path's config")
	writeConfig(t, file, "https://prod.invalid", "shared")
	// Same context name in another file must not make the copied target ambiguous.
	writeConfig(t, filepath.Join(source.Root, "prod", "other"), "https://other.invalid", "shared")
	clipboard := stubTransferClipboard(t)
	out, err := run(t, "prod", "--context", "shared", "--file", file)
	if err != nil {
		t.Fatal(err)
	}
	applySelection(t, out)
	deadline := strconv.FormatInt(time.Now().Add(10*time.Hour).Unix(), 10)
	t.Setenv("KCM_EXPIRES_AT", deadline)
	original, _ := os.ReadFile(file)
	for _, command := range []string{"copy", "cp"} {
		code, messages, err := transferRun(t, command)
		if err != nil || code != "" || !strings.Contains(messages, "Copied profile prod") {
			t.Fatalf("copy: %q %q %v", code, messages, err)
		}
	}
	content, err := os.ReadFile(clipboard)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if !strings.HasPrefix(text, selectionPrefix) {
		t.Fatal("clipboard does not contain a pasteable KCM command")
	}
	// Only the fixed command prefix contains spaces; the token is shell-safe ASCII.
	token := strings.TrimPrefix(text, selectionPrefix)
	for _, c := range token {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			t.Fatalf("unsafe token character: %q", c)
		}
	}
	// Simulate another window initialized against a different settings file.
	destination := filepath.Join(t.TempDir(), "destination.toml")
	if err := os.WriteFile(destination, []byte("kube_root = \"/fixture/unused\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"paste"}, {"paste", "--selection", token}} {
		t.Setenv("KCM_SETTINGS", destination)
		t.Setenv("KCM_PROFILE", "local")
		t.Setenv("KCM_CONTEXT", "")
		t.Setenv("KCM_FILE", "")
		t.Setenv("KCM_EXPIRES_AT", "")
		code, _, err := transferRun(t, args...)
		if err != nil {
			t.Fatal(err)
		}
		applySelection(t, code)
		if currentProfile() != "prod" || os.Getenv("KCM_CONTEXT") != "shared" || os.Getenv("KUBECONFIG") != file || os.Getenv("KCM_EXPIRES_AT") != deadline {
			t.Fatalf("paste did not reproduce source selection: %s", code)
		}
		if !strings.Contains(code, "export KCM_SETTINGS="+quote(source.Path)) {
			t.Fatal("settings path not transferred")
		}
	}
	after, _ := os.ReadFile(file)
	if !bytes.Equal(original, after) {
		t.Fatal("transfer rewrote source kubeconfig")
	}
	afterClipboard, _ := os.ReadFile(clipboard)
	if !bytes.Equal(content, afterClipboard) {
		t.Fatal("paste changed clipboard")
	}
	// An untimed source remains untimed even when its profile has a timeout.
	t.Setenv("KCM_SETTINGS", source.Path)
	t.Setenv("KCM_EXPIRES_AT", "")
	if _, _, err := transferRun(t, "cp"); err != nil {
		t.Fatal(err)
	}
	code, _, err := transferRun(t, "paste")
	if err != nil {
		t.Fatal(err)
	}
	applySelection(t, code)
	if os.Getenv("KCM_EXPIRES_AT") != "" {
		t.Fatal("paste introduced a timeout absent from source")
	}
}

func TestTransferCorruptionAndValidation(t *testing.T) {
	s := fixture(t)
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(s.Root, "dev", "config")
	writeConfig(t, path, "https://dev.invalid", "dev")
	clipboard := stubTransferClipboard(t)
	selection := copiedSelection{1, s.Path, "dev", "dev", path, "https://dev.invalid", ""}
	token := encodeSelection(selection)
	badVersion := selection
	badVersion.Version = 2
	malformed := []string{"", "unrelated text", "touch /fixture/do-not-run", "kcm paste", selectionPrefix, selectionPrefix + token + "; touch /fixture/do-not-run", selectionPrefix + token[:len(token)-1] + "!", selectionPrefix + encodeSelection(badVersion), selectionPrefix + strings.Repeat("x", maxSelectionSize+1)}
	for _, content := range malformed {
		if err := os.WriteFile(clipboard, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		code, _, err := transferRun(t, "paste")
		if err == nil || !strings.Contains(err.Error(), "clipboard does not contain a kcm profile + context") || code != "" {
			t.Fatalf("invalid clipboard: code=%q err=%v", code, err)
		}
	}
	// Valid checksums do not make unknown fields/versions or invalid paths valid.
	data, _ := json.Marshal(selection)
	data = append(data[:len(data)-1], []byte(",\"unexpected\":true}")...)
	digest := sha256.Sum256(data)
	if _, err := decodeSelection("kcm1." + base64.RawURLEncoding.EncodeToString(data) + "." + hex.EncodeToString(digest[:])); err == nil {
		t.Fatal("accepted unknown payload fields")
	}
	for _, mode := range []string{"expired", "missing profile", "missing context", "missing settings", "changed server"} {
		changed := selection
		switch mode {
		case "expired":
			changed.ExpiresAt = "1"
		case "missing profile":
			changed.Profile = "missing"
		case "missing context":
			changed.Context = "missing"
		case "missing settings":
			changed.Settings = filepath.Join(t.TempDir(), "missing.toml")
		case "changed server":
			changed.Server = "https://changed.invalid"
		}
		code, _, err := transferRun(t, "paste", "--selection", encodeSelection(changed))
		if err == nil || code != "" {
			t.Fatalf("accepted %s: %s %v", mode, code, err)
		}
	}
	if code, _, err := transferRun(t, "paste", "--settings", s.Path, "--selection", token); err == nil || code != "" {
		t.Fatalf("accepted settings override: %s %v", code, err)
	}
	if _, _, err := transferRun(t, "copy"); err == nil {
		t.Fatal("copied without selection")
	}
	out, err := run(t, "dev", "--context", "dev")
	if err != nil {
		t.Fatal(err)
	}
	applySelection(t, out)
	t.Setenv("KCM_EXPIRES_AT", "1")
	before, _ := os.ReadFile(clipboard)
	if _, _, err := transferRun(t, "copy"); err == nil {
		t.Fatal("copied expired selection")
	}
	after, _ := os.ReadFile(clipboard)
	if !bytes.Equal(before, after) {
		t.Fatal("failed copy changed clipboard")
	}
	t.Setenv("PATH", t.TempDir())
	if err := writeClipboard([]byte("fixture")); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("missing clipboard writer: %v", err)
	}
}
