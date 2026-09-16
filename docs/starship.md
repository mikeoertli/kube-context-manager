# Starship integration

This document contains information for integration with [Starship](https://github.com/starship/starship).

Copy the block below (also available as [examples/starship.toml](../examples/starship.toml))
into your `~/.config/starship.toml`. Replace the existing `[kubernetes]` block,
including its `contexts` rules. Remove the earlier `[custom.kcm]` example if
you added it, to avoid duplicate output. Keep your other Starship modules.

## Why profile-based styling?

KCM maps a profile to a kubeconfig directory. These modules check `KCM_PROFILE`,
so styling follows that mapping even with a custom `kube_root` or an absolute
profile directory. They do not check your working directory or guess the
environment from a context's name.

The built-in Kubernetes module reads the source file's `current-context`;
KCM keeps its selection in the shell and leaves that field unchanged. Disable
the built-in module and display `kcm prompt` instead. A context called
`customer-a` selected from the production profile gets production styling
without needing `prod` in its name.

| Profile | Default source directory | Default emoji | Suggested style |
| --- | --- | --- | --- |
| local | `~/.kube/`, direct files | 🏠 | Dimmed green |
| dev | `~/.kube/dev/` | 🛠️ | Yellow |
| qa | `~/.kube/qa/` | 🧪 | Bold bright red |
| prod | `~/.kube/prod/` | 🚨 | Bold magenta |
| other | `~/.kube/other/` | 🌐 | Bold orange |
| Additional profiles | Their configured directory | Their configured emoji | Bold yellow fallback |

## Suggested starship.toml block

```toml
# Merge into ~/.config/starship.toml; replace your existing [kubernetes] block.
# Remove an older [custom.kcm] block to avoid showing KCM twice.
# With a custom top-level format, include $custom (before any table headers).

[kubernetes]
disabled = true

# Default directory: ~/.kube/ (direct files only)
[custom.kcm_local]
when = 'test "${KCM_PROFILE:-}" = local'
command = "kcm prompt"
shell = ["sh"]
format = '[$output]($style) '
style = "dimmed green"

# Default directory: ~/.kube/dev/
[custom.kcm_dev]
when = 'test "${KCM_PROFILE:-}" = dev'
command = "kcm prompt"
shell = ["sh"]
format = '[$output]($style) '
style = "yellow"

# Default directory: ~/.kube/qa/
[custom.kcm_qa]
when = 'test "${KCM_PROFILE:-}" = qa'
command = "kcm prompt"
shell = ["sh"]
format = '[$output]($style) '
style = "bold bright-red"

# Default directory: ~/.kube/prod/
[custom.kcm_prod]
when = 'test "${KCM_PROFILE:-}" = prod'
command = "kcm prompt"
shell = ["sh"]
format = '[$output]($style) '
style = "bold #ff00ff"

# Default directory: ~/.kube/other/
[custom.kcm_other]
when = 'test "${KCM_PROFILE:-}" = other'
command = "kcm prompt"
shell = ["sh"]
format = '[$output]($style) '
style = "bold #ff8700"

# Any additional named profile, such as support, perf, or nightly.
[custom.kcm_fallback]
when = 'case "${KCM_PROFILE:-}" in ""|local|dev|qa|prod|other) exit 1 ;; *) exit 0 ;; esac'
command = "kcm prompt"
shell = ["sh"]
format = '[$output]($style) '
style = "bold yellow"
```

Exactly one KCM module matches an active profile. None matches when KCM has not
initialized the shell. The fallback keeps new profiles visible; add a dedicated
module and exclude its name from the fallback condition if you want a distinct
color for it.

### Placing it in your prompt

Starship's default `format = '$all'` already includes custom modules. If you
have a custom top-level `format`, include `$custom` where you want KCM to
appear (for example, where `$kubernetes` previously appeared). This also
includes any other custom modules you have.

To position only KCM modules, insert this sequence in your existing format:

```text
${custom.kcm_local}${custom.kcm_dev}${custom.kcm_qa}${custom.kcm_prod}${custom.kcm_other}${custom.kcm_fallback}
```

Top-level `format` belongs before table headers in TOML. Keep the rest of your
existing prompt format; the snippet does not replace it.

### Symbols, namespace, and timeout

Emoji come from `kcm_settings.toml`, so Starship and `kcm profiles` agree.
To use your previous red production symbol, for example:

```toml
# ~/.config/kcm/kcm_settings.toml
[profiles.prod]
emoji = "🔴"
```

Typical output:

```text
🏠 local · orbstack
🚨 prod · customer-a · 7h59m0s left
🏠 local · —
```

The final example is the state after expiry: local, with no context selected.
Starship displays the state; KCM's shell integration performs the reset.
The countdown refreshes when Starship renders a prompt, not continuously while
a prompt sits idle.

Currently `kcm prompt` shows profile, context, and an optional timeout.
It does **not** show namespace or user. Use `kcm status` for the shared
namespace. The old `user_pattern = ".*admin.*"` override is not reproduced:
the suggested color describes the selected profile, not the user's permissions.
Likewise, names containing `qa`, `perf`, or `nightly` do not override the
directory's profile style.

The `sh` subprocesses read the exported KCM state without loading your
interactive shell startup files. Ensure the `kcm` executable is on `PATH`.

References: [Starship custom commands](https://starship.rs/config/#custom-commands)
and [Kubernetes module](https://starship.rs/config/#kubernetes).
