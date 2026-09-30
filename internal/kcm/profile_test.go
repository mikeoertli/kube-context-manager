package kcm

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestProfilePickerThenContext(t *testing.T) {
	for _, mode := range []string{"success", "cancel-profile", "cancel-context", "empty", "invalid", "named", "context-only"} {
		t.Run(mode, func(t *testing.T) {
			s := fixture(t)
			local := filepath.Join(s.Root, "config")
			prod := filepath.Join(s.Root, "prod", "config")
			writeConfig(t, local, "https://localhost:6443", "local-context")
			if mode != "empty" {
				writeConfig(t, prod, "https://prod.example", "remote", "second")
			}
			if mode == "invalid" {
				if err := os.WriteFile(prod, []byte("contexts: [invalid"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			original, _ := os.ReadFile(prod)
			deadline := strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10)
			t.Setenv("KCM_CONTEXT", "local-context")
			t.Setenv("KCM_FILE", local)
			t.Setenv("KCM_EXPIRES_AT", deadline)
			t.Setenv("KCM_PREVIOUS_CONTEXT", "old-context")
			t.Setenv("KCM_PREVIOUS_FILE", "old-file")
			bin := t.TempDir()
			calls := filepath.Join(bin, "calls")
			t.Setenv("KCM_TEST_PICKER_CALLS", calls)
			t.Setenv("KCM_TEST_PICKER_MODE", mode)
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			stub := `#!/bin/sh
printf '%s\n' "$*" >> "$KCM_TEST_PICKER_CALLS"
case "$*" in
    *'--header=Profile for this shell'*)
        [ "$KCM_TEST_PICKER_MODE" = cancel-profile ] && exit 130
        needle=' prod ' ;;
    *'--header=Context · prod'*)
        [ "$KCM_TEST_PICKER_MODE" = cancel-context ] && exit 130
        needle='second ' ;;
    *'--header=Context · local'*) needle='local-context ' ;;
    *) exit 2 ;;
esac
while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in *"$needle"*) printf '%s\n' "$line"; exit 0 ;; esac
done
exit 2
`
			if err := os.WriteFile(filepath.Join(bin, "fzf"), []byte(stub), 0700); err != nil {
				t.Fatal(err)
			}
			args := []string{"profile"}
			if mode == "named" {
				args = append(args, "prod")
			} else if mode == "context-only" {
				args = []string{}
			}
			before := time.Now().Add(8 * time.Hour).Unix()
			out, err := run(t, args...)
			log, _ := os.ReadFile(calls)
			switch mode {
			case "success":
				if err != nil {
					t.Fatal(err)
				}
				for _, want := range []string{"export KCM_PROFILE='prod'", "export KCM_CONTEXT='second'", "export KUBECONFIG=" + quote(prod), "unset KCM_CONTEXT KCM_FILE KCM_EXPIRES_AT KCM_PREVIOUS_CONTEXT KCM_PREVIOUS_FILE", "_KCM_PROFILE_EMOJI='🚨'"} {
					if !strings.Contains(out, want) {
						t.Fatalf("missing %q in %s", want, out)
					}
				}
				var expires int64
				for _, line := range strings.Split(out, "\n") {
					if strings.HasPrefix(line, "export KCM_EXPIRES_AT=") {
						expires, _ = strconv.ParseInt(strings.TrimPrefix(strings.TrimSuffix(line, "'"), "export KCM_EXPIRES_AT='"), 10, 64)
					}
				}
				if expires < before || expires > time.Now().Add(8*time.Hour).Unix() {
					t.Fatalf("new profile inherited the old deadline: %s", out)
				}
				if strings.Contains(out, "old-context") || strings.Count(string(log), "--header=") != 2 {
					t.Fatalf("incorrect history or picker sequence: %s\n%s", out, log)
				}
			case "named":
				if err != nil || len(log) != 0 || strings.Contains(out, "export KCM_CONTEXT=") {
					t.Fatalf("named profile unexpectedly picked context: %s %v %s", out, err, log)
				}
			case "context-only":
				if err != nil || !strings.Contains(out, "export KCM_CONTEXT='local-context'") || strings.Count(string(log), "--header=") != 1 {
					t.Fatalf("plain kcm changed picker behavior: %s %v %s", out, err, log)
				}
			default:
				if err == nil || out != "" {
					t.Fatalf("unsuccessful selection emitted changes: %q %v", out, err)
				}
				if mode == "empty" && !strings.Contains(err.Error(), "no contexts available in profile") {
					t.Fatal(err)
				}
			}
			if currentProfile() != "local" || os.Getenv("KCM_CONTEXT") != "local-context" || os.Getenv("KCM_EXPIRES_AT") != deadline {
				t.Fatal("command changed process environment while preparing shell changes")
			}
			after, _ := os.ReadFile(prod)
			if !bytes.Equal(original, after) {
				t.Fatal("profile picker changed source kubeconfig")
			}
		})
	}
}
