# KCM terminal demo

[demo.tape](demo.tape) is the editable [VHS](https://github.com/charmbracelet/vhs)
sequence used to render [assets/demo.gif](../assets/demo.gif).

## Try the demo

```sh
make demo
```

Requires Go, zsh, fzf, and Python 3. The launcher starts a disposable shell with
synthetic kubeconfigs, separate settings, and no inherited KCM state. It does
not read your shell startup files or change your kubeconfigs. Type `exit` to
leave; the temporary directory is removed automatically.

The KCM binary, profile/context pickers, discovery, imports, namespace writes,
and expiry hooks are real. Only namespace discovery and creation use a small
fixture `kubectl` client. That client never forwards commands to an installed
kubectl. All server names end in `.invalid`, and the fixture configs contain no
credentials. Permissions checks and real workloads are not part of this demo.
The prompt displays KCM's exported text using zsh; Starship is not required.

Production expires after **six seconds in the demo settings**, so the recording
can show the stale-command cancellation. The normal default remains eight hours.

## Record the GIF

Install [VHS](https://github.com/charmbracelet/vhs), ffmpeg, and ttyd.
On macOS:

```sh
brew install vhs ffmpeg ttyd fzf
make demo-gif
```

Run from the repository root. This builds the current KCM source, validates the
tape, then overwrites `assets/demo.gif`. VHS needs a local terminal server and a
headless browser; its first run may download a browser. Nothing is published or
committed by this target.

The recording uses KCM's own picker highlighting: the active row has a bright
pink background and black text, including search matches. It pauses on each
choice before pressing Enter so viewers can follow the selection. This styling
also appears in normal KCM sessions and requires fzf 0.52 or newer. No key overlay,
experimental VHS fork, or subtitle-rendering dependency is needed.

The sequence shows:

1. Profiles and their discovered contexts with `kcm list`.
2. Profile selection followed directly by context selection.
3. Namespace selection and shared namespace status.
4. Importing and renaming a config into another profile.
5. Production expiry cancelling a submitted command and returning to local.

The tape uses screen-content waits at picker transitions and short pauses for
reading. Synthetic paths and countdown values can vary between recordings;
reproducibility means the same workflow, not byte-identical GIFs.

## Keep it current

When commands, picker behavior, shell hooks, displayed output, or demo fixtures
change, update `demo/demo.tape` as needed and run `make demo-gif` in the same
change. Also refresh it when a new capability should join the walkthrough.
Review the rendered GIF for legible text, uncropped menus, successful commands,
and the expiry reset. Keep it concise and use only generic, synthetic examples.
Documentation-only changes that do not affect the walkthrough do not need a
new recording. Keep the tape, supporting fixtures, GIF, and README in sync.
