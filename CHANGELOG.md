# Changelog

## 0.7.0 — Work in progress

- Automatically select a local context on expiry, preferring the file named
  `config`, then the first context by name/path. Disable with `auto_revert_local = false`.
- Keep stale-command cancellation and fall back to an unselected local profile
  when no suitable local context is available.

## 0.6.0 — 2026-10-08

- Add `kcm renew --for DURATION` and `--extend DURATION` for one-off shell deadlines.
- Add `kcm print-kubeconfig` (`kc`) and `kcm print-context-name` (`ctx`) for scripts.
  `ctx` now prints the selected name; use `kcm context NAME` to select a context.
- Format `kcm status` with terminal colors and aligned fields, show missing
  selections clearly, and explain namespace changes shared across shells.
- Rename the default settings file to `~/.config/kcm/kcm_config.toml`;
  existing users should rename `kcm_settings.toml` and update explicit paths.

## 0.5.0 — 2026-10-08

- Add a GitHub Actions workflow that notifies the Homebrew tap when a stable
  version tag is pushed, and document Homebrew installation and release setup.

## 0.4.0 — 2026-10-06

- Highlight the active picker row in bright pink with black text (requires fzf 0.52+).
- Add an isolated demo shell, a repeatable VHS recording with clear menu selections,
  and a README demo GIF.

## 0.3.0 — 2026-10-01

- Follow interactive profile selection with a context picker, applying both together.
- Preserve the current shell selection when either picker is cancelled or fails.

## 0.2.1 — in progress

- Use native Starship environment-variable modules to avoid prompt subprocess timeouts.
- Document migration, conditional display, custom profile colors, and a two-line Starship layout using generic examples.

## 0.2.0 — 2026-09-17

- Support external kubeconfig symlinks using their profile link paths.

- Ignore documentation and unrelated files during kubeconfig discovery.

- Embed the application version from `VERSION` for every build path.
- Add `kcm list` with contexts grouped by profile and global/shell selection markers.

## 0.1.0 — 2026-09-16

- Add on-demand `permissions` summaries and `can-i` action checks without caching.

- Name client wrappers `kcmkubectl`, `kcmhelm`, and `kcmk9s` to preserve standard commands.
- Add Go CLI, profile-scoped discovery, fzf selection, and shared namespace edits.
- Add zsh/bash integration with Enter-time expiry reset and command cancellation.
- Add configurable local classification, production timeouts, and root waivers.
- Add interactive and unattended imports with renaming, copy/move, and archives.
- Add completions, Starship prompt output, documentation, and project icon.
- Add namespace creation from the picker or `kcm ns NAME --create`.
