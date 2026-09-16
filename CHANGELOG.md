# Changelog

## 0.2.0 — in progress

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
