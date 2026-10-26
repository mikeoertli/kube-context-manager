# Changelog

## 0.9.0 — Work in progress

- Add `kcm <profile> --context <name>` for validated profile and context selection.
- Add `kcm reset` to select the default local context and clear previous selection history.

## 0.8.0 — 2026-10-21

- Shorten the README demo to the basic selection, namespace, and expiry workflow.

- Select the imported profile and context after successful imports; add
  `--import-only` to preserve the current shell selection.

- Move file imports by default; retain the source with `--import-copy`.
- Add `kcm import --clipboard` with private temporary staging, validation,
  and current-directory resolution for relative credential references.
- Add `kcm import` with newest Desktop/Downloads discovery, searchable directory
  selection, profile/name options, and forced or skipped readability updates.
- Keep file pickers ordered by modification time, newest first, including while searching.
- Add interactive and scripted `kcm rename` (`make-readable`) for readable
  cluster, user, and context names, preserving all entries and shared references.
- Back up originals before in-place edits, reject collisions, preserve symlinks,
  and refresh renamed shell selections without renewing their timeout.

## 0.7.0 — 2026-10-21

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
