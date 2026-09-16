<p align="center">
  <img src="assets/icon.png" alt="kube_context_manager project icon" width="400" height="400">
</p>

# kube-context-manager (`kcm`)

Keep Kubernetes profiles and context selection in your shell. Leave a terminal
in production overnight, and KCM returns it to `local` before your next command
runs.

KCM uses **shared kubeconfig files**, **shell environment variables**, and **fzf**.
It does not create per-shell kubeconfig copies or change a source file's
`current-context`. Namespace changes are shared between shells using that context.

## Install

Requires Go 1.25+, [fzf](https://github.com/junegunn/fzf), and zsh or bash 4.4+.
`kubectl` is needed for namespace discovery. No gum or yq dependency.

```sh
git clone https://github.com/mikeoertli/kube_context_manager.git
cd kube_context_manager
make install                     # installs kcm to GOBIN or GOPATH/bin
kcm settings init                # optional; built-in defaults also work
```

Ensure the Go bin directory is on `PATH`. On macOS, `brew install go fzf` provides
the dependencies; `brew install bash` provides a modern bash if desired. Apple's
bundled bash 3.2 does not support KCM's Enter-time expiry integration.

Add this **after shell plugins and keybindings** in `~/.zshrc`:

```sh
eval "$(kcm init zsh)"
```

For bash, use `eval "$(kcm init bash)"` in `~/.bashrc` and ensure your interactive
login shell sources that file. Open a new shell after setup.

## Everyday use

```sh
kcm                         # fuzzy-select a local context
kcm profile                 # fuzzy-select a profile for this shell
kcm profile prod            # or select explicitly
kcm context customer-a
kubectl get pods             # shell wrapper supplies --context customer-a
helm list                    # wrapper supplies --kube-context customer-a
k9s                          # wrapper supplies --context customer-a
kcm ns                       # choose an existing namespace or create a new one
kcm ns itrs                  # update this context's shared namespace
kcm ns feature-demo --create # create in the selected cluster, then switch
kcm status                   # profile, context, namespace, expiry
kcm profile local            # explicitly leave production
```

Every new shell starts in `local`, including a new interactive child shell.
Changing profiles clears the context. **Choose a context before using a client.**
With no selection, `KUBECONFIG=/dev/null` prevents fallback to a shared default
config. Selecting a context sets `KUBECONFIG` to its source file.

| Profile | Default directory | Default timeout |
| --- | --- | --- |
| 🏠 `local` | `~/.kube/` (direct files) | Disabled |
| 🛠️ `dev` | `~/.kube/dev/` | Disabled |
| 🧪 `qa` | `~/.kube/qa/` | Disabled |
| 🚨 `prod` | `~/.kube/prod/` | 8 hours |
| 🌐 `other` | `~/.kube/other/` | Disabled |

Directories are scanned non-recursively. Hidden files, subdirectories, backup
files ending in `~` or `.bak`, and certificate/key files (`.pem`, `.crt`, `.key`)
are excluded. Other regular files must be valid kubeconfigs. Symlinked files
must resolve within their profile directory.

### Returning home after a timeout

The timer starts when you select a context with a timeout. Changing contexts
does not extend an existing deadline; `kcm renew` explicitly restarts it.

- At a **stale prompt**, pressing Enter resets the profile to `local`, clears
  the context and `KUBECONFIG` selection, and **cancels the entire submitted
  command line**. A notice tells you what happened.
- If time expires while a command is running, KCM resets at the next prompt.
- Nothing runs in the background. Credentials are not expired or revoked.
  Running tools and scripts continue normally.

This is an interactive-shell reminder, not an access-control boundary. Custom
widgets that bypass `accept-line`, other shell integrations that replace KCM's
Enter bindings, and commands sent without the normal line editor bypass that
check. KCM does not wrap every client invocation with a timeout gate.

## Import kubeconfigs

```sh
kcm install ~/Downloads/customer.yaml       # interactive wizard

kcm install ./customer.yaml --profile prod \
  --rename-cluster customer-a \
  --rename-user customer-a-support-ro \
  --rename-context customer-a \
  --namespace itrs --no-prompt

kcm install ./local.yaml --no-prompt        # local endpoint: defaults to local
kcm install ./remote.yaml --profile other --no-prompt --move
kcm install ./bundle.yaml --profile dev --all-contexts --no-prompt --dry-run
```

The wizard selects a source context, destination profile, readable names,
namespace, filename, and copy/move behavior, then shows a confirmation. All
choices have matching flags. Missing rename flags preserve names with
`--no-prompt`; a remote config requires an explicit `--profile`. Interactive
imports suggest `other` for otherwise unclassified remote endpoints.

For multi-context files, use `--context NAME` or `--all-contexts`. A selected
context imports only its referenced cluster and user. Renaming is available
for single-context imports. `--namespace` can apply to every imported context.

Imports never overwrite destination files. They embed referenced certificates
and keys and preserve token-file and exec paths as absolute paths. External
token files and auth helpers must remain available. Copy is the default;
`--move` requires importing every source context, verifies the installed config,
and archives the original under `kube_root/archive/` before removing the source.
New config and archive files have mode `0600`.

## Namespace picker and creation

`kcm context`, `kcm profile`, and `kcm namespace` (alias `ns`) open fzf pickers
when called without a name. The namespace picker queries the selected cluster
and includes **Create new namespace…**. Choose it, enter a name, and KCM creates
the namespace before switching. The name prompt identifies the profile and
context; a blank name or Ctrl-C cancels.

Use `kcm ns NAME --create` to create and switch directly, or `kcm ns --create`
to prompt only for the new name. Creation requires cluster permission. If it
fails (including when the name already exists), the configured namespace stays
unchanged. `kcm ns NAME` only changes the shared config; it neither creates a
namespace nor checks whether it exists. All namespace switches remain shared
between shells using that source context.

## Settings and local-only checks

The settings file is `~/.config/kcm/kcm_settings.toml`. See the complete
[example settings](internal/kcm/defaults.toml), which KCM also uses as its defaults.
Override the root, add profiles, choose emoji, or disable a timeout:

```toml
kube_root = "~/kubeconfigs"
local_hosts = ["kubernetes.docker.internal"]
local_cidrs = ["192.168.49.2/32"]

[profiles.prod]
timeout = "0"                 # disabled; normal default is "8h"

[profiles.support]
dir = "support"               # relative to kube_root; absolute paths work too
emoji = "🧰"
timeout = "2h"
```

`localhost` and loopback IPs are local by default. Private network addresses are
not automatically local, and classification never performs DNS lookups. An
omitted timeout defaults to eight hours for `prod` or a selected source under
`kube_root/prod/`; other profiles have no timeout. An explicit profile timeout
takes precedence. Settings changes apply on the next selection or `kcm renew`.

Non-local contexts at the root produce an error until moved or waived:

```sh
kcm doctor
kcm waive deliberately-local-tunnel
kcm waivers
kcm unwaive deliberately-local-tunnel
```

Waivers live in `kube_root/.kcm_waivers` as readable JSON. Each binds to the
context name, canonical source path, and server address. Changing any of those
requires a new waiver. Discovery never relocates files automatically.

Use `KCM_SETTINGS=/path/to/settings.toml` or `--settings PATH` for alternate
settings. For a shell that always uses an alternate file:

```sh
eval "$(kcm --settings /path/to/settings.toml init zsh)"
```

## Completions and Starship

Zsh (after `compinit`) or bash:

```sh
source <(kcm completion zsh)
# source <(kcm completion bash)
```

Completion includes profile names, context names, and flags. Fish and PowerShell
completion generation is also available; session integration supports zsh/bash.

For Starship, use the [suggested config block](docs/starship.md) or copy
[examples/starship.toml](examples/starship.toml) into your `~/.config/starship.toml`.
It replaces the built-in `[kubernetes]` module and old context/user regex rules
with styles based on the profile-to-directory mapping: local green, dev yellow,
qa red, prod magenta, and other orange, with a fallback for custom profiles.

The guide covers custom prompt placement, emoji overrides, and migration from
the previous `[custom.kcm]` snippet. It uses `kcm prompt` for the shell-selected
context and timeout; namespace remains available through `kcm status`.

## Command reference

| Command | Function |
| --- | --- |
| `kcm`, `kcm context [name\|-]` | Select a context; `-` returns to the previous one |
| `kcm profile [name]` | Select this shell's profile |
| `kcm namespace [name] [--create]` | Pick a shared namespace, or create and switch |
| `kcm contexts`, `kcm profiles` | List available contexts or profiles |
| `kcm status`, `kcm prompt` | Detailed status or compact prompt output |
| `kcm clear`, `kcm renew` | Clear selection or restart its timer |
| `kcm install PATH` | Import, rename, copy, or move configs |
| `kcm waive NAME`, `kcm unwaive NAME`, `kcm waivers` | Manage root exceptions |
| `kcm doctor` | Validate settings and config layout |
| `kcm init SHELL`, `kcm completion SHELL` | Generate integration and completions |
| `kcm settings init`, `kcm settings path` | Create or locate settings |
| `kcm exec -- CLIENT ARGS` | Pass the selected context to kubectl, helm, or k9s |
| `kcm version`, `kcm help [COMMAND]` | Version and help |

Every command supports `--help`. Aliases: `ctx`, `ns`. See
[complete command help](docs/commands.md) and [behavior details](docs/design.md).

## Development

```sh
make build                  # bin/kcm
make check                  # formatting, vet, unit/integration tests
python3 tests/shell_integration.py ./bin/kcm --bash /path/to/modern/bash --fzf /path/to/fzf
```

Tests use temporary fixture configs and do not contact a Kubernetes cluster.
Go dependencies are pinned in `go.mod` and verified by the committed `go.sum`.
`VERSION` tracks the development version. No GitHub Actions, release jobs, or
tagging workflow are included.
