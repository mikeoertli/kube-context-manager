#!/usr/bin/env python3
"""Real PTY checks for Enter-time expiry; no cluster access or user config reads.

Run: python3 tests/shell_integration.py ./bin/kcm [--bash /path/to/bash]
Requires zsh; bash must be 4.4+. All fixtures live in a temporary directory.
"""
import argparse
import fcntl
import json
import os
import pathlib
import pty
import select
import shlex
import shutil
import signal
import struct
import tempfile
import termios
import time


class Shell:
    def __init__(self, executable, kind, binary, settings):
        self.pid, self.fd = pty.fork()
        if self.pid == 0:
            env = dict(os.environ, TERM="xterm-256color", KCM_SETTINGS=str(settings))
            env.pop("ENV", None)
            env.pop("BASH_ENV", None)
            args = [executable, "-f", "-i"] if kind == "zsh" else [executable, "--noprofile", "--norc", "-i"]
            os.execve(executable, args, env)
        fcntl.ioctl(self.fd, termios.TIOCSWINSZ, struct.pack("HHHH", 30, 140, 0, 0))
        self.drain(0.3)
        self.send("PS1='KCM_TEST> '; " + ('unsetopt prompt_sp; ' if kind == 'zsh' else '') +
                  'eval "$(' + shlex.quote(str(binary)) + ' init ' + kind + ')"')
        output = self.prompt()
        assert "invalid keymap" not in output and "kcm:" not in output, output

    def drain(self, duration):
        end = time.monotonic() + duration
        output = b""
        while time.monotonic() < end:
            ready, _, _ = select.select([self.fd], [], [], min(0.1, max(0, end - time.monotonic())))
            if ready:
                try:
                    chunk = os.read(self.fd, 65536)
                    output += chunk
                    # fzf queries the terminal cursor and bracketed-paste mode.
                    # A PTY supplies bytes, not a terminal emulator, so reply.
                    for _ in range(chunk.count(b"\x1b[6n")):
                        os.write(self.fd, b"\x1b[24;1R")
                    for _ in range(chunk.count(b"\x1b[?2004$p")):
                        os.write(self.fd, b"\x1b[?2004;1$y")
                except OSError:
                    break
        return output

    def send(self, command, enter=b"\r"):
        os.write(self.fd, command.encode() + enter)

    def prompt(self):
        output = b""
        end = time.monotonic() + 8
        while time.monotonic() < end:
            output += self.drain(0.15)
            if b"KCM_TEST> " in output:
                output += self.drain(0.1)
                return output.decode(errors="replace")
        raise AssertionError("No prompt: " + output.decode(errors="replace"))

    def command(self, command):
        self.send(command)
        return self.prompt()

    def close(self):
        os.kill(self.pid, signal.SIGKILL)
        os.waitpid(self.pid, 0)
        os.close(self.fd)


def check(executable, kind, binary, settings, base, mode, enter):
    shell = Shell(executable, kind, binary, settings)
    try:
        if mode == "vi":
            shell.command("bindkey -v" if kind == "zsh" else "set -o vi")
        output = shell.command("kcm profile prod; kcm context remote")
        assert "kcm:" not in output, output
        state = base / (kind + "-" + mode + "-state")
        shell.command("printf '%s|%s' \"$KCM_PROFILE\" \"$KCM_CONTEXT\" > " + shlex.quote(str(state)))
        assert state.read_text() == "prod|remote", state.read_text()
        shell.command("printf '%s|%s' \"$KCM_PROMPT_ACTIVE_VAR\" \"$KCM_PROMPT_TEXT_prod\" > " + shlex.quote(str(state)))
        assert state.read_text().startswith("KCM_PROMPT_TEXT_prod|🚨 prod · remote · "), state.read_text()
        assert state.read_text().endswith(" left"), state.read_text()
        # Let expiry occur while the shell is already parked at a prompt.
        clock = "$EPOCHSECONDS" if kind == "zsh" else "$(printf '%(%s)T' -1)"
        shell.command("export KCM_EXPIRES_AT=$(( " + clock + " + 2 ))")
        time.sleep(2.2)
        first, second = base / "first-command-ran", base / "second-command-ran"
        shell.send("printf wrong > " + shlex.quote(str(first)) + "; printf wrong | cat > " + shlex.quote(str(second)), enter)
        output = shell.prompt()
        assert "submitted command cancelled" in output, output
        assert not first.exists() and not second.exists(), "Part of expired command executed"
        shell.command("printf '%s|%s|%s' \"$KCM_PROFILE\" \"$KUBECONFIG\" \"${KCM_CONTEXT:-}\" > " + shlex.quote(str(state)))
        assert state.read_text() == "local|/dev/null|", state.read_text()
        shell.command("printf '%s|%s' \"$KCM_PROMPT_TEXT_local\" \"${KCM_PROMPT_TEXT_prod:-}\" > " + shlex.quote(str(state)))
        assert state.read_text() == "🏠 local · —|", state.read_text()
        # Prompt-time expiry after a running command must reset without killing it.
        shell.command("kcm profile prod; kcm context remote")
        output = shell.command("export KCM_EXPIRES_AT=1; printf completed > " + shlex.quote(str(state)))
        assert state.read_text() == "completed"
        assert "session expired" in output, output
        print("PASS", kind, mode, "Enter=" + repr(enter), "stale prompt, compound cancellation, reset, running command")
    finally:
        shell.close()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("binary", type=pathlib.Path)
    parser.add_argument("--bash")
    parser.add_argument("--fzf", help="Optional fzf executable for actual picker/wizard checks")
    parser.add_argument("--only-picker", action="store_true", help="Run only fzf and wizard checks")
    args = parser.parse_args()
    binary = args.binary.resolve()
    with tempfile.TemporaryDirectory(prefix="kcm-shell-test-") as tmp:
        base = pathlib.Path(tmp)
        root = base / "kube"
        (root / "prod").mkdir(parents=True)
        settings = base / "settings.toml"
        settings.write_text("kube_root = " + json.dumps(str(root)) + "\n")
        config = {"apiVersion": "v1", "kind": "Config", "clusters": [{"name": "cluster", "cluster": {"server": "https://fixture.invalid"}}],
                  "users": [{"name": "user", "user": {"token": "fixture"}}],
                  "contexts": [{"name": n, "context": {"cluster": "cluster", "user": "user"}} for n in ["remote", "second"]], "current-context": "second"}
        (root / "prod" / "config").write_text(json.dumps(config))
        zsh = shutil.which("zsh")
        if not zsh:
            raise RuntimeError("zsh required")
        targets = [(zsh, "zsh")]
        if args.bash:
            targets.append((str(pathlib.Path(args.bash).resolve()), "bash"))
        if not args.only_picker:
            for executable, kind in targets:
                for mode, enter in [("emacs", b"\r"), ("emacs", b"\n"), ("vi", b"\r")]:
                    check(executable, kind, binary, settings, base, mode, enter)
                check_isolation(executable, kind, binary, settings, base, root)
        if args.fzf:
            check_picker(zsh, binary, settings, base, root, pathlib.Path(args.fzf).resolve())
        if not args.bash and not args.only_picker:
            print("SKIP bash PTY tests: supply --bash /path/to/bash (4.4+)")


def check_isolation(executable, kind, binary, settings, base, root):
    a = Shell(executable, kind, binary, settings)
    b = Shell(executable, kind, binary, settings)
    try:
        config = root / "prod" / "config"
        before = config.read_bytes()
        a.command("kcm --settings " + shlex.quote(str(settings)) + " profile prod; kcm context remote")
        b.command("kcm profile prod; kcm context second")
        a_state, b_state = base / "a-state", base / "b-state"
        a.command("printf '%s' \"$KCM_CONTEXT\" > " + shlex.quote(str(a_state)))
        b.command("printf '%s' \"$KCM_CONTEXT\" > " + shlex.quote(str(b_state)))
        assert a_state.read_text() == "remote" and b_state.read_text() == "second"
        assert config.read_bytes() == before, "Selection mutated a shared source"
        fakebin = base / "clients"
        fakebin.mkdir(exist_ok=True)
        for client in ["kubectl", "helm", "k9s"]:
            stub = fakebin / client
            stub.write_text('#!/bin/sh\nprintf "%s\\n" "$KUBECONFIG" "$@"\n')
            stub.chmod(0o700)
        a.command("export PATH=" + shlex.quote(str(fakebin)) + ":$PATH")
        for client, flag in [("kubectl", "--context"), ("helm", "--kube-context"), ("k9s", "--context")]:
            a.command(client + " get pods > " + shlex.quote(str(a_state)))
            assert a_state.read_text().splitlines() == [str(config), "get", "pods"], "Standard client was wrapped"
            a.command("kcm" + client + " get pods > " + shlex.quote(str(a_state)))
            assert a_state.read_text().splitlines() == [str(config), flag, "remote", "get", "pods"]
            b.command("export PATH=" + shlex.quote(str(fakebin)) + ":$PATH")
            b.command("kcm" + client + " get pods > " + shlex.quote(str(b_state)))
            assert b_state.read_text().splitlines() == [str(config), flag, "second", "get", "pods"]
            a.command("kcm" + client + " " + flag + " override 'two words' > " + shlex.quote(str(a_state)))
            assert a_state.read_text().splitlines() == [str(config), flag, "remote", flag, "override", "two words"]
        a.command("kcm ns shared-test")
        a.command("kcm status > " + shlex.quote(str(a_state)))
        assert "shared-test" in a_state.read_text()
        # A fresh child shell resets inherited production env state.
        child = 'eval "$(' + shlex.quote(str(binary)) + ' init ' + kind + ')"; printf "%s|%s" "$KCM_PROFILE" "$KUBECONFIG"'
        a.command(shlex.quote(executable) + " -c " + shlex.quote(child) + " > " + shlex.quote(str(a_state)))
        assert a_state.read_text() == "local|/dev/null", a_state.read_text()
        # Normal CLI errors cannot apply partial state to the parent shell.
        a.command("kcm profile missing-profile")
        a.command("printf '%s' \"$KCM_PROFILE\" > " + shlex.quote(str(a_state)))
        assert a_state.read_text() == "prod"
        a.command("kcm clear")
        for client in ["kubectl", "helm", "k9s"]:
            a.command("kcm" + client + " version > " + shlex.quote(str(a_state)))
            assert a_state.read_text().splitlines() == ["/dev/null", "version"]
        # Reinitializing preserves pre-existing client functions and aliases.
        for client in ["kubectl", "helm", "k9s"]:
            a.command(client + "() { printf 'user-function'; }")
            a.command("alias " + client + "='printf user-alias'")
        a.command('eval "$(' + shlex.quote(str(binary)) + ' init ' + kind + ')"')
        for client in ["kubectl", "helm", "k9s"]:
            a.command(client + " > " + shlex.quote(str(a_state)))
            assert a_state.read_text() == "user-alias"
            a.command("unalias " + client)
            a.command(client + " > " + shlex.quote(str(a_state)))
            assert a_state.read_text() == "user-function"
            a.command("kcm" + client + " version > " + shlex.quote(str(a_state)))
            assert a_state.read_text().splitlines() == ["/dev/null", "version"]
        print("PASS", kind, "two-shell isolation, prefixed wrappers, standard clients/aliases/functions preserved, namespace, child reset, errors")
    finally:
        a.close()
        b.close()


def check_picker(executable, binary, settings, base, root, fzf):
    shell = Shell(executable, "zsh", binary, settings)
    try:
        shell.command("export PATH=" + shlex.quote(str(fzf.parent)) + ":$PATH")
        shell.send("kcm profile")
        output = shell.drain(0.5)
        assert b"Profile for this shell" in output, output
        shell.send("prod", enter=b"")
        shell.drain(0.3)
        shell.send("")
        profile_output = shell.prompt()
        state = base / "picker-state"
        shell.command("printf '%s' \"$KCM_PROFILE\" > " + shlex.quote(str(state)))
        assert state.read_text() == "prod", (state.read_text(), profile_output)
        shell.send("kcm")
        output = shell.drain(0.5)
        assert b"Context" in output, output
        shell.send("remote", enter=b"")
        shell.drain(0.3)
        shell.send("")
        shell.prompt()
        shell.command("printf '%s|%s' \"$KCM_PROFILE\" \"$KCM_CONTEXT\" > " + shlex.quote(str(state)))
        assert state.read_text() == "prod|remote", state.read_text()
        shell.send("kcm profile")
        shell.drain(0.5)
        os.write(shell.fd, b"\x03")
        shell.prompt()
        shell.command("printf '%s|%s' \"$KCM_PROFILE\" \"$KCM_CONTEXT\" > " + shlex.quote(str(state)))
        assert state.read_text() == "prod|remote", "Cancel changed shell state"
        # Exercise namespace choices with a fake API client and the real fzf UI.
        nsbin = base / "namespace-clients"
        nsbin.mkdir()
        nslog = base / "namespace-api-calls"
        stub = nsbin / "kubectl"
        stub.write_text('#!/bin/sh\nprintf "%s\\n" CALL "$@" >> ' + shlex.quote(str(nslog)) +
                        '\nif [ "$5" = get ]; then printf "default\\nkube-system\\n"; '
                        'elif [ "$5" = create ]; then printf "namespace/%s created\\n" "$7"; else exit 2; fi\n')
        stub.chmod(0o700)
        shell.command("export PATH=" + shlex.quote(str(nsbin)) + ":$PATH")
        for query, new_name in [("kube-system", None), ("Create new", "fresh-demo"), ("Create new", "")]:
            shell.send("kcm ns")
            output = shell.drain(0.5)
            assert b"Namespace" in output and b"Create new namespace" in output, output
            shell.send(query, enter=b"")
            shell.drain(0.3)
            shell.send("")
            if new_name is not None:
                output = shell.drain(0.4)
                assert b"New namespace to create in prod / remote" in output, output
                shell.send(new_name)
            output = shell.prompt()
            if new_name == "":
                assert "namespace creation cancelled" in output, output
            shell.command("kcm status > " + shlex.quote(str(state)))
            expected = "kube-system" if new_name is None else "fresh-demo"
            assert "Namespace: " + expected + " (shared)" in state.read_text(), state.read_text()
        calls = nslog.read_text()
        assert calls.count("\ncreate\nnamespace\nfresh-demo\n") == 1, calls
        assert "--context\nremote\ncreate\nnamespace\nfresh-demo\n" in calls, calls
        print("PASS zsh namespace picker: existing selection, create and switch, blank-name cancellation (fake API)")
        source = base / "wizard.yaml"
        # This standalone fixture remains JSON even after namespace tests rewrite prod YAML.
        source.write_text(json.dumps({"apiVersion": "v1", "kind": "Config",
            "clusters": [{"name": "old", "cluster": {"server": "https://wizard.invalid"}}],
            "users": [{"name": "old", "user": {"token": "fixture"}}],
            "contexts": [{"name": "old", "context": {"cluster": "old", "user": "old"}}], "current-context": "old"}))
        original = source.read_bytes()
        shell.send("kcm install " + shlex.quote(str(source)) + " --profile other --rename-cluster cluster --rename-user user --rename-context readable --namespace app --filename wizard.yaml --copy")
        output = shell.drain(0.5)
        assert b"Install? (yes/no)" in output, output
        shell.send("yes")
        shell.prompt()
        assert source.read_bytes() == original
        assert (root / "other" / "wizard.yaml").exists()
        shell.command("kcm profile other; kcm contexts --names > " + shlex.quote(str(state)))
        assert state.read_text().strip() == "readable"
        print("PASS zsh actual fzf profile/context selection, cancellation, prefilled install wizard")
    finally:
        shell.close()


if __name__ == "__main__":
    main()
