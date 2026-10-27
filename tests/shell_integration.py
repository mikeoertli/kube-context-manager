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
import re
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
            env = dict(os.environ, TERM="xterm-256color", KCM_SETTINGS=str(settings), KCM_PROMPT_ENABLED="1")
            env.pop("KCM_STARSHIP_PROFILES", None)
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
            check_import(zsh, binary, settings, base, root, pathlib.Path(args.fzf).resolve())
        if not args.only_picker:
            for executable, kind in targets:
                check_local_return(executable, kind, binary, settings, base, root)
                check_shortcuts(executable, kind, binary, settings, base, root, args.fzf)
                check_transfer(executable, kind, binary, settings, base, root)
                check_rename(executable, kind, binary, settings, base, root)
        if not args.bash and not args.only_picker:
            print("SKIP bash PTY tests: supply --bash /path/to/bash (4.4+)")


def check_import(executable, binary, settings, base, root, fzf):
    directory = base / "download-fixtures"
    directory.mkdir()
    sources = []
    for name, age in [("a-old.kubeconfig", 7200), ("z-new.kubeconfig", 60)]:
        path = directory / name
        path.write_text(json.dumps({"apiVersion": "v1", "kind": "Config",
            "clusters": [{"name": "opaque/generated/cluster", "cluster": {"server": "https://import.invalid"}}],
            "users": [{"name": "opaque-user", "user": {"token": "fixture"}}],
            "contexts": [{"name": "arn:example:cluster/dev", "context": {"cluster": "opaque/generated/cluster", "user": "opaque-user"}}],
            "current-context": "arn:example:cluster/dev"}))
        modified = time.time() - age
        os.utime(path, (modified, modified))
        sources.append((path, path.read_bytes()))
    shell = Shell(executable, "zsh", binary, settings)
    try:
        shell.command("export PATH=" + shlex.quote(str(fzf.parent)) + ":$PATH")
        shell.send("kcm import --dir " + shlex.quote(str(directory)) + " --profile qa --name imported")
        output = shell.drain(0.6)
        assert b"newest first" in output and output.find(b"z-new.kubeconfig") < output.find(b"a-old.kubeconfig"), output
        shell.send("z-new", enter=b"")
        shell.drain(0.3)
        shell.send("")
        output = shell.drain(0.4)
        assert b"Make them readable?" in output, output
        shell.send("no")
        assert b"Import? (yes/no)" in shell.drain(0.3)
        shell.send("no")
        assert "cancelled" in shell.prompt()
        assert not (root / "qa" / "imported").exists()

        # Accept automatically offered readability and the suggested label defaults.
        shell.send("kcm import --file " + shlex.quote(str(sources[1][0])) + " --profile qa --name imported --import-copy")
        assert b"Make them readable?" in shell.drain(0.4)
        shell.send("yes")
        for label in [b"Readable cluster name [imported]", b"Readable user name [imported-user]", b"Readable context name [imported]"]:
            output = shell.drain(0.3)
            assert label in output, output
            shell.send("")
        assert b"Import? (yes/no)" in shell.drain(0.3)
        shell.send("yes")
        shell.prompt()
        state = base / "import-state"
        shell.command("printf '%s|%s' \"$KCM_PROFILE\" \"${KCM_CONTEXT:-}\" > " + shlex.quote(str(state)))
        assert state.read_text() == "qa|imported", "Import did not activate the imported selection"
        shell.command("kcm ctx > " + shlex.quote(str(state)))
        assert state.read_text() == "imported\n"

        # Explicit skip suppresses the readability offer, while filename remains editable.
        shell.send("kcm import --file " + shlex.quote(str(sources[0][0])) + " --profile dev --skip-readable")
        output = shell.drain(0.4)
        assert b"Import name" in output and b"Make them readable" not in output, output
        shell.send("preserved")
        output = shell.drain(0.3)
        assert b"Import? (yes/no)" in output and b"Readable" not in output, output
        shell.send("no")
        shell.prompt()
        shell.command("printf '%s|%s' \"$KCM_PROFILE\" \"$KCM_CONTEXT\" > " + shlex.quote(str(state)))
        assert state.read_text() == "qa|imported", "Cancelled import changed selection"
        for path, original in sources:
            assert path.read_bytes() == original, "Import changed a download"

        # Default imports move only after archiving the exact source bytes.
        path, original = sources[0]
        shell.command("kcm import --file " + shlex.quote(str(path)) + " --profile dev --name moved --skip-readable --no-prompt --import-only")
        assert not path.exists(), "Default import retained its source"
        assert (root / "dev" / "moved").exists()
        backups = list((root / "archive").glob("moved.original-*"))
        assert len(backups) == 1 and backups[0].read_bytes() == original
        shell.command("printf '%s|%s' \"$KCM_PROFILE\" \"$KCM_CONTEXT\" > " + shlex.quote(str(state)))
        assert state.read_text() == "qa|imported", "--import-only changed selection"

        # Stub every supported reader so tests never touch the real clipboard.
        clipboard_bin = base / "clipboard-bin"
        clipboard_bin.mkdir()
        clipboard = sources[1][0]
        for name in ["pbpaste", "wl-paste", "xclip", "xsel"]:
            reader = clipboard_bin / name
            reader.write_text("#!/bin/sh\nexec /bin/cat " + shlex.quote(str(clipboard)) + "\n")
            reader.chmod(0o700)
        shell.command("export PATH=" + shlex.quote(str(clipboard_bin)) + ":$PATH")
        output = shell.command("kcm import --clipboard --profile dev --name pasted --readable --no-prompt")
        assert "Source: clipboard" in output and "kcm-clipboard-" not in output, output
        assert (root / "dev" / "pasted").exists()
        shell.command("printf '%s|%s|%s' \"$KCM_PROFILE\" \"$KCM_CONTEXT\" \"$KUBECONFIG\" > " + shlex.quote(str(state)))
        assert state.read_text() == "dev|pasted|" + str(root / "dev" / "pasted"), "Clipboard import did not activate"
        assert clipboard.read_bytes() == sources[1][1], "Clipboard import changed its content"
        print("PASS zsh import: searchable picker, readability, cancellation, copy, move with backup, clipboard activation, import-only")
    finally:
        shell.close()


def check_rename(executable, kind, binary, settings, base, root):
    path = root / "dev" / (kind + "-rename.yaml")
    path.parent.mkdir(exist_ok=True)
    config = {"apiVersion": "v1", "kind": "Config",
        "clusters": [{"name": "opaque-cluster", "cluster": {"server": "https://rename.invalid"}}],
        "users": [{"name": "opaque-user", "user": {"token": "fixture"}}],
        "contexts": [{"name": name, "context": {"cluster": "opaque-cluster", "user": "opaque-user", "namespace": "default"}} for name in ["opaque", "peer"]],
        "current-context": "opaque"}
    path.write_text(json.dumps(config))
    original = path.read_bytes()
    shell = Shell(executable, kind, binary, settings)
    try:
        state = base / (kind + "-rename-state")
        shell.command("kcm profile dev; kcm context peer; kcm context opaque; kcm renew --for 24h")
        shell.command("printf '%s' \"$KCM_EXPIRES_AT\" > " + shlex.quote(str(state)))
        deadline = state.read_text()
        output = shell.command("kcm rename --rename-cluster dev --rename-user reader --rename-context api-dev --namespace app --no-prompt")
        assert "Original backup:" in output and "Updated kubeconfig:" in output, output
        shell.command("kcm ctx > " + shlex.quote(str(state)))
        assert state.read_text() == "api-dev\n", "Shell did not refresh the renamed context"
        shell.command("printf '%s' \"$KCM_EXPIRES_AT\" > " + shlex.quote(str(state)))
        assert state.read_text() == deadline, "Rename renewed the timeout"
        backups = list((root / "archive").glob(path.name + ".original-*"))
        assert len(backups) == 1 and backups[0].read_bytes() == original
        shell.command("kcm rename --context peer --rename-context previous --no-prompt")
        shell.command("printf '%s|%s' \"$KCM_CONTEXT\" \"$KCM_PREVIOUS_CONTEXT\" > " + shlex.quote(str(state)))
        assert state.read_text() == "api-dev|previous", state.read_text()

        # Exercise every missing-name prompt, then cancel at confirmation.
        before = path.read_bytes()
        backup_count = len(list((root / "archive").glob("*")))
        shell.send("kcm make-readable")
        assert b"Cluster name [dev]" in shell.drain(0.4)
        shell.send("friendly-cluster")
        assert b"User name [reader]" in shell.drain(0.3)
        shell.send("")
        assert b"Context name [api-dev]" in shell.drain(0.3)
        shell.send("friendly-context")
        assert b"Apply changes?" in shell.drain(0.3)
        shell.send("no")
        assert "rename cancelled" in shell.prompt()
        assert path.read_bytes() == before and len(list((root / "archive").glob("*"))) == backup_count

        shell.send("kcm make-readable --rename-cluster friendly-cluster --rename-user reader --rename-context friendly-context")
        assert b"Apply changes?" in shell.drain(0.4)
        shell.send("yes")
        shell.prompt()
        shell.command("kcm ctx > " + shlex.quote(str(state)))
        assert state.read_text() == "friendly-context\n"

        # A source edited while the confirmation is open must not be overwritten.
        external = base / "changed.yaml"
        external.write_bytes(original)
        backup_count = len(list((root / "archive").glob("*")))
        shell.send("kcm rename " + shlex.quote(str(external)) + " --rename-cluster new --rename-user reader --rename-context new")
        assert b"Apply changes?" in shell.drain(0.4)
        config["clusters"][0]["cluster"]["server"] = "https://changed.invalid"
        external.write_text(json.dumps(config))
        changed = external.read_bytes()
        shell.send("yes")
        assert "source changed during rename" in shell.prompt()
        assert external.read_bytes() == changed and len(list((root / "archive").glob("*"))) == backup_count
        print("PASS", kind, "rename: backup, active/previous selection refresh, unchanged timeout, prompts, cancellation, source-change rejection")
    finally:
        shell.close()


def check_local_return(executable, kind, binary, settings, base, root):
    local_file = root / "config"
    local_file.write_text(json.dumps({"apiVersion": "v1", "kind": "Config",
        "clusters": [{"name": "local", "cluster": {"server": "https://127.0.0.1:6443"}}],
        "contexts": [{"name": "local-lab", "context": {"cluster": "local"}}],
        "current-context": "local-lab"}))
    original = local_file.read_bytes()
    original_settings = settings.read_text()
    a = Shell(executable, kind, binary, settings)
    b = Shell(executable, kind, binary, settings)
    try:
        a.command("kcm profile prod; kcm context remote")
        b.command("kcm profile prod; kcm context second")
        state = base / (kind + "-local-return")
        clock = "$EPOCHSECONDS" if kind == "zsh" else "$(printf '%(%s)T' -1)"
        a.command("export KCM_EXPIRES_AT=$(( " + clock + " + 2 ))")
        time.sleep(2.2)
        cancelled = base / (kind + "-cancelled-after-return")
        a.send("printf wrong > " + shlex.quote(str(cancelled)))
        output = a.prompt()
        assert "local context local-lab" in output and "submitted command cancelled" in output, output
        assert not cancelled.exists(), "Expired command ran after local restoration"
        a.command("kcm kc; kcm ctx")
        a.command("printf '%s|%s|%s|%s|%s|%s' \"$KCM_PROFILE\" \"$KCM_CONTEXT\" \"$KUBECONFIG\" \"${KCM_EXPIRES_AT:-}\" \"${KCM_PREVIOUS_CONTEXT:-}\" \"$KCM_PROMPT_TEXT_local\" > " + shlex.quote(str(state)))
        assert state.read_text() == "local|local-lab|" + str(local_file) + "|||🏠 local · local-lab", state.read_text()
        b.command("printf '%s|%s' \"$KCM_PROFILE\" \"$KCM_CONTEXT\" > " + shlex.quote(str(state)))
        assert state.read_text() == "prod|second", "Return changed another shell"
        assert local_file.read_bytes() == original, "Return edited the local kubeconfig"

        # Settings are read at expiry, including changes made after shell init.
        settings.write_text(original_settings + "\nauto_revert_local = false\n")
        a.command("kcm profile prod; kcm context remote")
        a.command("export KCM_EXPIRES_AT=1")
        a.command("printf '%s|%s|%s' \"$KCM_PROFILE\" \"${KCM_CONTEXT:-}\" \"$KUBECONFIG\" > " + shlex.quote(str(state)))
        assert state.read_text() == "local||/dev/null", state.read_text()

        settings.write_text(original_settings)
        a.command("kcm profile prod; kcm context remote")
        settings.write_text("malformed = [\n")
        a.command("export KCM_EXPIRES_AT=1")
        a.command("printf '%s|%s|%s' \"$KCM_PROFILE\" \"${KCM_CONTEXT:-}\" \"$KUBECONFIG\" > " + shlex.quote(str(state)))
        assert state.read_text() == "local||/dev/null", "Failed settings load retained remote state"
        print("PASS", kind, "automatic local return, cancellation, shell isolation, config opt-out, invalid-settings fallback")
    finally:
        a.close()
        b.close()
        settings.write_text(original_settings)


def check_shortcuts(executable, kind, binary, settings, base, root, fzf):
    a = Shell(executable, kind, binary, settings)
    b = Shell(executable, kind, binary, settings)
    source = root / "prod" / "config"
    before = source.read_bytes()
    state = base / (kind + "-shortcuts")
    snapshot = "printf '%s|%s|%s|%s' \"$KCM_PROFILE\" \"$KCM_CONTEXT\" \"$KUBECONFIG\" \"${KCM_EXPIRES_AT:-}\" > " + shlex.quote(str(state))
    try:
        a.command("kcm --settings " + shlex.quote(str(settings)) + " prod --context=remote")
        a.command(snapshot)
        selected = state.read_text()
        assert selected.startswith("prod|remote|" + str(source) + "|")
        assert selected.rsplit("|", 1)[1].isdigit(), "Shorthand did not start timeout"
        b.command("kcm prod --context second")
        a.command("kcm prod --context second")
        for command, expected in [("kcm -", "remote"), ("kcm -", "second"), ("kcm context -", "remote")]:
            a.command(command)
            a.command(snapshot)
            assert state.read_text().startswith("prod|" + expected + "|"), "Previous-context toggle failed"
        a.command(snapshot)
        selected = state.read_text()
        for command, message in [("kcm missing --context remote", "unknown profile"), ("kcm prod --context missing", "not found"), ("kcm prod", "specify --context")]:
            output = a.command(command)
            assert message in output, output
            a.command(snapshot)
            assert state.read_text() == selected, "Invalid shorthand changed selection"
        output = a.command("kcm __complete prod --context re")
        assert "remote" in output and "command not found" not in output, output
        a.command("kcm reset")
        a.command(snapshot)
        assert state.read_text() == "local|local-lab|" + str(root / "config") + "|"
        output = a.command("kcm -")
        assert "no previous context" in output, output
        a.command("kcm prod --context remote")
        a.command("kcm local --context local-lab")
        a.command("kcm -")
        a.command(snapshot)
        assert state.read_text().startswith("prod|remote|"), "Cross-profile toggle failed"
        a.command("kcm -")
        a.command(snapshot)
        assert state.read_text() == "local|local-lab|" + str(root / "config") + "|"
        b.command(snapshot)
        assert state.read_text().startswith("prod|second|"), "Reset changed another shell"
        a.command("kcm prod --context second")
        a.command("kcm local --context local-lab")
        listing = base / (kind + "-history-list")
        a.command("kcm history --list > " + shlex.quote(str(listing)))
        text = listing.read_text()
        assert "second" in text and "remote" in text and text.index("second") < text.index("remote"), text
        assert "export " not in text, "List contained shell exports"
        a.command("kcm history 2")
        a.command(snapshot)
        assert state.read_text().startswith("prod|remote|"), "History number chose wrong entry"
        if fzf:
            a.command("export PATH=" + shlex.quote(str(pathlib.Path(fzf).resolve().parent)) + ":$PATH")
            a.send("kcm history")
            assert b"Recent selections" in a.drain(0.5)
            a.send("second", enter=b"")
            a.drain(0.3)
            a.send("")
            a.prompt()
            a.command(snapshot)
            assert state.read_text().startswith("prod|second|"), "History picker chose wrong selection"
            a.send("kcm history")
            a.drain(0.4)
            a.send("\x03", enter=b"")
            output = a.prompt()
            assert "selection cancelled" in output, output
            a.command(snapshot)
            assert state.read_text().startswith("prod|second|"), "Cancelled history picker changed selection"
        assert source.read_bytes() == before, "Shorthand rewrote current-context"
        print("PASS", kind, "profile shorthand, toggle, history list/number/picker, validation, timeout, completions, reset, isolation")
    finally:
        a.close()
        b.close()


def check_transfer(executable, kind, binary, settings, base, root):
    clipboard = base / (kind + "-transfer-clipboard")
    clipboard.write_text("")
    clipboard_bin = base / (kind + "-transfer-bin")
    clipboard_bin.mkdir()
    for name in ["pbpaste", "pbcopy", "wl-paste", "wl-copy", "xclip", "xsel"]:
        if name in ["pbcopy", "wl-copy"]:
            body = '#!/bin/sh\n/bin/cat > "$KCM_TEST_CLIPBOARD"\n'
        elif name in ["xclip", "xsel"]:
            body = '#!/bin/sh\ncase " $* " in *" -in "*|*" --input "*) /bin/cat > "$KCM_TEST_CLIPBOARD" ;; *) /bin/cat "$KCM_TEST_CLIPBOARD" ;; esac\n'
        else:
            body = '#!/bin/sh\n/bin/cat "$KCM_TEST_CLIPBOARD"\n'
        reader = clipboard_bin / name
        reader.write_text(body)
        reader.chmod(0o700)
    a = Shell(executable, kind, binary, settings)
    b = Shell(executable, kind, binary, settings)
    state = base / (kind + "-transfer-state")
    snapshot = "printf '%s|%s|%s|%s|%s|%s' \"$KCM_SETTINGS\" \"$KCM_PROFILE\" \"$KCM_CONTEXT\" \"$KCM_FILE\" \"$KUBECONFIG\" \"${KCM_EXPIRES_AT:-}\" > " + shlex.quote(str(state))
    source = root / "prod" / "config"
    before = source.read_bytes()
    try:
        for shell in [a, b]:
            shell.command("export PATH=" + shlex.quote(str(clipboard_bin)) + ":$PATH KCM_TEST_CLIPBOARD=" + shlex.quote(str(clipboard)))
        a.command("kcm prod --context remote; kcm renew --for 24h")
        a.command(snapshot)
        expected = state.read_text()
        output = a.command("kcm cp")
        assert "Copied profile prod" in output, output
        copied = clipboard.read_text()
        assert copied.startswith("kcm paste --selection kcm1.") and "\n" not in copied
        b.command("kcm local --context local-lab")
        output = b.command("kcm paste")
        assert "from the copied selection" in output, output
        b.command(snapshot)
        assert state.read_text() == expected, "Paste changed source timeout/settings/file"
        assert clipboard.read_text() == copied, "Paste modified clipboard"
        b.command("kcm prod --context second")
        # These are the same bytes a literal terminal paste inserts before Enter.
        b.send(copied)
        output = b.prompt()
        assert "from the copied selection" in output, output
        b.command(snapshot)
        assert state.read_text() == expected, "Literal paste did not restore source state"
        marker = base / "corrupted-paste-ran"
        for corrupt in ["", "unrelated clipboard", copied[:-1] + "x", copied + "; touch " + shlex.quote(str(marker))]:
            clipboard.write_text(corrupt)
            output = b.command("kcm paste")
            assert "clipboard does not contain a kcm profile + context" in output, output
            b.command(snapshot)
            assert state.read_text() == expected, "Invalid clipboard changed destination"
        assert not marker.exists(), "Paste executed clipboard content"
        a.command(snapshot)
        assert state.read_text() == expected, "Transfer modified source shell"
        assert source.read_bytes() == before, "Transfer modified shared kubeconfig"
        print("PASS", kind, "clipboard copy/paste, literal paste, exact deadline/settings/file, corruption, source isolation")
    finally:
        a.close()
        b.close()


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
        # Renewal and read-only aliases operate in the parent shell, with no effect on its peer.
        b.command("printf '%s' \"$KCM_EXPIRES_AT\" > " + shlex.quote(str(b_state)))
        peer_deadline = b_state.read_text()
        a.command("kcm renew --for 96h; printf '%s' \"$KCM_EXPIRES_AT\" > " + shlex.quote(str(a_state)))
        deadline = int(a_state.read_text())
        assert abs(deadline - int(time.time()) - 96 * 3600) < 5
        a.command("kcm renew --extend 24h; printf '%s' \"$KCM_EXPIRES_AT\" > " + shlex.quote(str(a_state)))
        assert int(a_state.read_text()) == deadline + 24 * 3600
        b.command("printf '%s' \"$KCM_EXPIRES_AT\" > " + shlex.quote(str(b_state)))
        assert b_state.read_text() == peer_deadline, "Renew changed the other shell"
        for command, expected in [("kc", str(config)), ("print-kubeconfig", str(config)), ("ctx", "remote"), ("print-context-name", "remote")]:
            a.command("kcm " + command + " > " + shlex.quote(str(a_state)))
            assert a_state.read_text() == expected + "\n", a_state.read_text()
        a.command("kcm renew; printf '%s' \"$KCM_EXPIRES_AT\" > " + shlex.quote(str(a_state)))
        assert abs(int(a_state.read_text()) - int(time.time()) - 8 * 3600) < 5
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
        output = shell.drain(0.5)
        assert b"Context" in output and b"prod" in output, output
        state = base / "picker-state"
        shell.send("remote", enter=b"")
        shell.drain(0.3)
        shell.send("")
        shell.prompt()
        shell.command("printf '%s|%s' \"$KCM_PROFILE\" \"$KCM_CONTEXT\" > " + shlex.quote(str(state)))
        assert state.read_text() == "prod|remote", state.read_text()
        # Cancelling the second picker preserves the original shell selection.
        shell.send("kcm profile")
        shell.drain(0.5)
        shell.send("prod", enter=b"")
        shell.drain(0.3)
        shell.send("")
        output = shell.drain(0.5)
        assert b"Context" in output, output
        os.write(shell.fd, b"\x03")
        shell.prompt()
        shell.command("printf '%s|%s' \"$KCM_PROFILE\" \"$KCM_CONTEXT\" > " + shlex.quote(str(state)))
        assert state.read_text() == "prod|remote", "Context cancellation changed shell state"
        # Plain kcm still opens only the current profile's context picker.
        shell.send("kcm")
        output = shell.drain(0.5)
        assert b"Context" in output and b"Profile for this shell" not in output, output
        shell.send("remote", enter=b"")
        shell.drain(0.3)
        shell.send("")
        shell.prompt()
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
            assert re.search(r"(?m)^  Namespace\s+" + re.escape(expected) + r"$", state.read_text()), state.read_text()
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
