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
and expiry hooks are real. Namespace discovery and creation use a small
fixture `kubectl` client, and clipboard readers return a synthetic kubeconfig.
The demo never reads or changes your real clipboard or forwards commands to an
installed kubectl. All server names end in `.invalid`, and the fixture configs
contain no credentials. Permissions checks and real workloads are not part of this demo.
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
4. Production expiry cancelling a submitted command and automatically restoring
   the local context, with its kubeconfig path visible in status.

The tape uses screen-content waits at picker transitions and short pauses for
reading. Synthetic paths and countdown values can vary between recordings;
reproducibility means the same workflow, not byte-identical GIFs.

## Keep it current

Keep the GIF focused on the basics: discovery, selection, namespaces, and expiry.
It does not need to demonstrate every feature. Detailed commands and advanced
workflows belong in the usage documentation.

When a change affects a demonstrated command, picker, shell hook, displayed
output, or fixture, update `demo/demo.tape` as needed and run `make demo-gif` in
the same change. Features outside the walkthrough do not require expanding or
re-recording the GIF. Review the result for legible text, uncropped menus,
successful commands, and the expiry reset. Use only generic, synthetic examples.
Keep the tape, supporting fixtures, GIF, and README in sync.
