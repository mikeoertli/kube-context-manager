#!/usr/bin/env bash
# Start a disposable demo shell; never source the user's shell configuration.
set -euo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
for dependency in zsh fzf python3; do
    command -v "$dependency" >/dev/null || { echo "Missing demo dependency: $dependency" >&2; exit 1; }
done
if [[ ! -x "$repo/bin/kcm" ]]; then
    echo 'Build KCM first: make build' >&2
    exit 1
fi
runtime=$(mktemp -d /tmp/kcm-demo.XXXXXX)
trap 'rm -rf -- "$runtime"' EXIT
python3 "$repo/demo/fixtures.py" "$runtime"
printf 'source %q\n' "$repo/demo/shell.zsh" > "$runtime/.zshrc"
# env -i drops inherited KCM state, credentials, shell hooks and fzf options.
env -i PATH="$repo/demo/bin:$repo/bin:$PATH" TERM="${TERM:-xterm-256color}" \
    LANG=en_US.UTF-8 ZDOTDIR="$runtime" KCM_SETTINGS="$runtime/settings.toml" \
    KCM_DEMO_DIR="$runtime" KCM_PROMPT_ENABLED=1 zsh -d -i
