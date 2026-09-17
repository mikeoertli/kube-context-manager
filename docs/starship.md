# Starship integration

This document contains information for integration with [Starship](https://github.com/starship/starship).

Copy the block below (also available as [examples/starship.toml](../examples/starship.toml))
into your `~/.config/starship.toml`. Replace the existing `[kubernetes]` block,
including its `contexts` rules. Remove **all** old `[custom.kcm*]` blocks, including
per-profile blocks; otherwise their commands will continue running. Keep your
other Starship modules.

## Why profile-based styling?

KCM maps a profile to a kubeconfig directory. KCM prepares prompt text from `KCM_PROFILE`,
so styling follows that mapping even with a custom `kube_root` or an absolute
profile directory. The modules do not check your working directory or guess the
environment from a context's name.

The built-in Kubernetes module reads the source file's `current-context`;
KCM keeps its selection in the shell and leaves that field unchanged. Disable
the built-in module and display KCM’s exported prompt text instead. A context called
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
# Remove ALL old [custom.kcm*] blocks so their shell commands stop running.
# With a custom top-level format, include $env_var (before any table headers).
# Requires the updated `kcm init zsh` / `kcm init bash` shell integration.

[kubernetes]
disabled = true

# Default directory: ~/.kube/ (direct files only)
[env_var.kcm_local]
variable = "KCM_PROMPT_TEXT_local"
format = '[$env_value]($style) '
style = "dimmed green"

# Default directory: ~/.kube/dev/
[env_var.kcm_dev]
variable = "KCM_PROMPT_TEXT_dev"
format = '[$env_value]($style) '
style = "yellow"

# Default directory: ~/.kube/qa/
[env_var.kcm_qa]
variable = "KCM_PROMPT_TEXT_qa"
format = '[$env_value]($style) '
style = "bold bright-red"

# Default directory: ~/.kube/prod/
[env_var.kcm_prod]
variable = "KCM_PROMPT_TEXT_prod"
format = '[$env_value]($style) '
style = "bold #ff00ff"

# Default directory: ~/.kube/other/
[env_var.kcm_other]
variable = "KCM_PROMPT_TEXT_other"
format = '[$env_value]($style) '
style = "bold #ff8700"

# Additional profiles, unless added to KCM_STARSHIP_PROFILES.
[env_var.kcm_fallback]
variable = "KCM_PROMPT_TEXT_fallback"
format = '[$env_value]($style) '
style = "bold yellow"
```

KCM's existing shell hook prepares the profile, context, emoji, and countdown
using shell built-ins. Starship reads the active environment variable directly:
**no per-profile shell checks, `kcm` subprocesses, config reads, or cluster requests
while rendering the prompt.** The standalone `kcm prompt` command still works.

After installing the updated binary, open a new shell to load the new integration.
Re-evaluating `kcm init` in an existing shell also works, but resets it to local
with no context selected. In zsh, initialize Starship before KCM so Starship's
hook captures the command's exit status first; the prompt renders after both hooks.

At most one KCM prompt text variable is exported at a time. None is set in a shell
that has not initialized KCM (a child process can still inherit its parent's
environment). Custom profiles use the fallback style unless configured below.
Do not add `default` values to these modules: missing variables hide inactive profiles.

### Placing it in your prompt

Starship's default `format = '$all'` already includes environment-variable modules. If you
have a custom top-level `format`, include `$env_var` where you want KCM to
appear (for example, where `$kubernetes` previously appeared). This also
includes any other environment-variable modules you have.

To position only KCM modules, insert this sequence in your existing format:

```text
${env_var.kcm_local}${env_var.kcm_dev}${env_var.kcm_qa}${env_var.kcm_prod}${env_var.kcm_other}${env_var.kcm_fallback}
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
The countdown refreshes at each shell prompt, not continuously while a prompt
sits idle. Calling Starship manually displays the most recently prepared text.

Currently `kcm prompt` shows profile, context, and an optional timeout.
It does **not** show namespace or user. Use `kcm status` for the shared
namespace. The old `user_pattern = ".*admin.*"` override is not reproduced:
the suggested color describes the selected profile, not the user's permissions.
Likewise, names containing `qa`, `perf`, or `nightly` do not override the
directory's profile style.

### Conditional availability

To display KCM only when both executables resolve, put this in your shell startup
file before `eval "$(kcm init zsh)"` (use `bash` for Bash):

```sh
export KCM_PROMPT_ENABLED=0
if command -v kubectl >/dev/null 2>&1 && command -v kcm >/dev/null 2>&1; then
    export KCM_PROMPT_ENABLED=1
fi
```

The shell checks availability once at startup. Each prompt then checks only the
flag using a shell built-in. No Starship `when` conditions are needed. Set the
flag to `0` to hide KCM; set it to `1` to show it again at the next prompt.
When unset, display is enabled by default. This flag controls display only;
profile selection and expiry still work when it is disabled.

### Custom profile colors

For a dedicated `perf` color, add this to your shell startup
file before KCM initialization (space-separated profile names):

```sh
export KCM_STARSHIP_PROFILES='local dev qa prod other perf'
```

Then add the corresponding Starship module:

```toml
[env_var.kcm_perf]
variable = "KCM_PROMPT_TEXT_perf"
format = '[$env_value]($style) '
style = "bold #ff8700"
```

With an explicit module sequence in your top-level format, add
`${env_var.kcm_perf}` too. `$env_var` includes it
automatically. Every name in `KCM_STARSHIP_PROFILES` needs a matching module;
other names use the fallback. Dedicated names support ASCII letters, digits,
and underscores; other profile names remain visible through the fallback.
Profile emoji settings refresh at initialization, profile/context selection, and
renewal. After editing emoji settings, reselect the profile or context to refresh.

### Diagnosing other slow modules

The old custom modules started a shell for every `when` expression, including
inactive profiles, then another for the selected `kcm prompt` command. Even a
small `test` can time out if process startup or scheduling is slow. The native
modules above remove those processes rather than increasing `command_timeout`.

A timeout executing Git is separate. Run `starship timings` in the affected
repository and outside it to identify slow modules. If `git_status` is the
culprit, temporarily set `[git_status] disabled = true` in your Starship config
to confirm it. This hides that module; it does not fix the repository's Git
performance. Do not assume the KCM change will resolve a Git timeout.

References: [Starship environment variables](https://starship.rs/config/#environment-variable),
[custom commands](https://starship.rs/config/#custom-commands), and
[Kubernetes module](https://starship.rs/config/#kubernetes).
