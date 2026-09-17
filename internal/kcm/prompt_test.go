package kcm

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestPromptEnvironment(t *testing.T) {
	for _, shell := range []string{"zsh", "bash"} {
		t.Run(shell, func(t *testing.T) {
			bin, err := exec.LookPath(shell)
			if shell == "bash" && os.Getenv("KCM_TEST_BASH") != "" {
				bin, err = os.Getenv("KCM_TEST_BASH"), nil
			}
			if err != nil {
				t.Skip(shell + " unavailable")
			}
			if shell == "bash" {
				if err := exec.Command(bin, "--noprofile", "--norc", "-c", "(( BASH_VERSINFO[0] > 4 || (BASH_VERSINFO[0] == 4 && BASH_VERSINFO[1] >= 4) ))").Run(); err != nil {
					t.Skip("requires Bash 4.4+; set KCM_TEST_BASH to its executable")
				}
			}
			s := fixture(t)
			var script bytes.Buffer
			s.emitPromptMetadata(&script, "local")
			// Simulate inherited production display in a freshly initialized child.
			script.WriteString("unset KCM_PROMPT_ENABLED KCM_STARSHIP_PROFILES\nexport KCM_PROMPT_ACTIVE_VAR=KCM_PROMPT_TEXT_prod KCM_PROMPT_TEXT_prod=stale\n")
			script.WriteString(commonShell)
			script.WriteString(`
fail() { printf 'FAIL: %s\n' "$1"; exit 1; }
# All prompt updates below must work with no executable available on PATH.
PATH=/nonexistent
[ "$KCM_PROMPT_TEXT_local" = '🏠 local · —' ] || fail initial
[ "${KCM_PROMPT_TEXT_prod+x}" != x ] || fail inherited
export KCM_PROFILE=prod KCM_CONTEXT='$(do-not-execute) [customer]'
_KCM_PROFILE_EMOJI=🔴
if [ -n "${ZSH_VERSION:-}" ]; then now=$EPOCHSECONDS; else printf -v now '%(%s)T' -1; fi
KCM_EXPIRES_AT=$((now + 3661))
_kcm_update_prompt
case "$KCM_PROMPT_TEXT_prod" in '🔴 prod · $(do-not-execute) [customer] · 1h1m'*s' left') ;; *) fail countdown ;; esac
[ "${KCM_PROMPT_TEXT_local+x}" != x ] || fail old_slot
KCM_PROFILE=perf KCM_EXPIRES_AT=''
_kcm_update_prompt
case "$KCM_PROMPT_TEXT_fallback" in *'perf · '*) ;; *) fail fallback ;; esac
KCM_STARSHIP_PROFILES='local dev qa prod other perf'
_kcm_update_prompt
[ "$KCM_PROMPT_ACTIVE_VAR" = KCM_PROMPT_TEXT_perf ] || fail custom
[ "${KCM_PROMPT_TEXT_fallback+x}" != x ] || fail old_fallback
KCM_PROFILE='has-dashes'
_kcm_update_prompt
[ "$KCM_PROMPT_ACTIVE_VAR" = KCM_PROMPT_TEXT_fallback ] || fail unusual_name
KCM_PROMPT_ENABLED=0
_kcm_update_prompt
[ "${KCM_PROMPT_TEXT_fallback+x}" != x ] || fail disabled
[ "${KCM_PROMPT_ACTIVE_VAR+x}" != x ] || fail disabled_marker
KCM_PROMPT_ENABLED=''
_kcm_update_prompt
[ "${KCM_PROMPT_ACTIVE_VAR+x}" != x ] || fail empty_disabled
KCM_PROMPT_ENABLED=1 KCM_PROFILE=prod KCM_EXPIRES_AT=1
_kcm_expire 2>/dev/null
[ "$KCM_PROMPT_TEXT_local" = '🏠 local · —' ] || fail expiry
[ "${KCM_PROMPT_TEXT_prod+x}" != x ] || fail expired_slot
KCM_EXPIRES_AT=invalid
_kcm_update_prompt
[ "$KCM_PROMPT_TEXT_local" = '🏠 local · —' ] || fail invalid_expiry
printf OK
`)
			args := []string{"-f", "-c", script.String()}
			if shell == "bash" {
				args = []string{"--noprofile", "--norc", "-c", script.String()}
			}
			cmd := exec.Command(bin, args...)
			cmd.Env = filteredEnv("ENV", "BASH_ENV")
			out, err := cmd.CombinedOutput()
			if err != nil || string(out) != "OK" {
				t.Fatalf("prompt update: %s (%v)", out, err)
			}
		})
	}
}

func TestPromptMetadataQuoting(t *testing.T) {
	s := fixture(t)
	s.Profiles["local"] = Profile{Dir: ".", Emoji: "'$(literal)"}
	var out bytes.Buffer
	s.emitPromptMetadata(&out, "local")
	if strings.Count(out.String(), quote("'$(literal)")) != 2 {
		t.Fatalf("symbols not shell-quoted: %s", out.String())
	}
}
