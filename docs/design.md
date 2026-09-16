# Behavior and boundaries

## Shared files, isolated selection

KCM stores profile selection, context name, source path, previous selection, and
an absolute expiry timestamp in the shell environment. It does not create a
session directory, copy kubeconfigs, or start a daemon.

The public `kcm` shell function captures shell assignments for `profile`,
`context`, `clear`, and `renew`, and applies them to its own shell. Assignments
are single-quoted and escaped. Cancelling a selector or encountering an error
applies no assignments. Other commands run as ordinary Go processes.

The source file's `current-context` remains untouched by selection. Small shell
functions named `kcmkubectl`, `kcmhelm`, and `kcmk9s` supply the selected context
flag to `kubectl`, `helm`, and `k9s`, respectively. They do not check timeouts.
Explicit caller flags take precedence. Normal namespace
changes update the selected context's namespace in the shared source file.

Noninteractive child processes inherit the environment but generally do not
inherit shell functions. Use `kcm exec -- kubectl ...`, `kcm exec -- helm ...`, or
`kcm exec -- k9s ...` in scripts, or pass the relevant context flag yourself.
For arbitrary clients, use `KCM_CONTEXT` with that client's context option.
`KUBECONFIG` alone does not convey KCM's selection within a multi-context file.
Standard `kubectl`, `helm`, and `k9s` commands are not wrapped or replaced, and
existing aliases and functions keep their definitions. These commands, absolute
client paths, and tools that read the source file's current-context directly
use that stored context unless explicitly told otherwise. The prefixed wrappers
invoke the client executable through `command`, bypassing shell functions.

Profile directories do not inherit from each other. Scanning never merges all
profiles, changes existing source configs, or classifies and moves existing
files automatically. Files may contain multiple contexts. Identical context
names in different files are disambiguated by source path in the switcher or
`kcm context NAME --file PATH`.

## Timeout lifecycle

1. A new interactive shell starts in local with no selection.
2. Selecting a timed context sets an absolute Unix timestamp in
   `KCM_EXPIRES_AT`.
3. Context changes retain an earlier unexpired deadline if the newly selected
   context is timed. Untimed selections clear the timer.
4. `kcm renew` applies the current profile settings and restarts the deadline.
5. Switching profiles or clearing selection clears the timer.
6. Expiry clears context, file, previous selection, and deadline, sets profile
   to local, and sets `KUBECONFIG=/dev/null`.

Zsh wraps the `accept-line` ZLE widget. Bash uses a readline macro that runs a
`bind -x` function before `accept-line`. If the deadline has passed, the handler
clears the complete line and prints a cancellation notice. This avoids running
either part of a semicolon list or a pipeline after a stale production prompt.
Prompt hooks also reset after an already-running command finishes.

This is intentionally a reminder for an interactive shell. It does not stop
long-running commands, revoke tokens, police explicit `--kubeconfig` flags, or
intercept scripts. Time is wall-clock time, so it includes sleep/overnight time
and follows system-clock adjustments. Load KCM after plugins that change Enter
bindings. Custom widgets and alternate execution bindings that bypass the normal
Enter handler are outside this behavior. For bash, Ctrl-X Ctrl-K and Ctrl-X
Ctrl-J are reserved for the check/accept macro.

KCM does not restore the pre-KCM `KUBECONFIG` when returning to local: that value
could point at production. An empty selection consistently uses `/dev/null`.

## Imports and validation

Kubeconfigs are decoded and written using Kubernetes' Go client library.
Parsing does not execute credential plugins. Interactive namespace discovery
and namespace creation invoke kubectl, which can invoke the selected config's
auth plugin. The namespace picker lists existing namespaces and offers a
creation action. `kcm ns NAME --create` creates in the explicitly selected
context before updating the shared config. Creation failure or cancellation
leaves the selection unchanged. If creation succeeds but writing the config
fails, KCM reports that the namespace exists and selection failed; it does not
delete the newly created namespace. A name without `--create` only updates the
config and does not contact the cluster.

Imported files are published only once fully written, using exclusive creation
to avoid overwriting an existing file. Sources are left untouched for copies.
Move operations refuse a partial multi-context import and symlink sources. They
check that the source contents have not changed during the wizard and archive
the original before deleting it. Certificate/key files are embedded. Token-file
and executable references are made absolute, not copied or removed.

Imports into the root must contain only locally classified endpoints. Existing
root files may contain non-local contexts only with explicit waivers. All root
contexts must pass validation, even when selecting another profile, so malformed
or misplaced configs remain visible rather than silently disappearing.

Discovery skips hidden files, directories, backup suffixes, certificate/key
extensions, and documentation names/extensions. Other files are considered only
if their contents identify a kubeconfig (`kind: Config` or top-level kubeconfig
fields), or their filename is `config`, `kubeconfig`, `*.kubeconfig`, or
`kubeconfig.*`. Unrelated text, YAML, and JSON are ignored. Recognizable malformed
kubeconfigs still fail validation; explicit imports always parse strictly.
Symlinked kubeconfigs may point outside the mapped directory. Profile membership,
selection matching, timeout classification, and waiver identity use the absolute
link path without resolving its target. Multiple links to one target remain
separate entries. Mapped profile directories may also be symlinks. Broken links
produce errors; links to directories are not recursively scanned. The local
profile must map to `.`.

`KCM_FILE`, `KUBECONFIG`, list output, and client calls retain the link path.
Namespace edits resolve the physical target only for the atomic write, preserving
the symlink and updating the shared repo file. Relative credential file references
are resolved by clients relative to the link's directory; KCM does not rewrite
those references. Root waivers bind to the link path, context, and server; they
do not apply automatically to other links to the same target. Legacy waivers
that recorded a resolved symlink target must be recreated for the link.

Writes use mode 0600 and new directories mode 0700. Existing directory modes are
not altered. Namespace writes replace the shared file atomically, so readers
see complete YAML. Like other kubeconfig editors, simultaneous writers can
overwrite each other's changes; avoid editing a source with multiple tools at
once. KCM is not a lock or credential isolation boundary.

## Permission inspection

`kcm permissions` sends a SelfSubjectRulesReview for the inspected namespace.
`kcm can-i` performs live discovery to resolve the resource and scope, then sends
a SelfSubjectAccessReview for the exact verb, resource, optional name, and
subresource. The client pins the source file and context explicitly. These checks
run only on request, do not cache permissions or discovery, and never change
selection, namespaces, or expiry. Listing, selection, and prompt code does not
invoke them. See [permission checks](permissions.md) for output and exit codes.

## Listing every profile

`kcm list` validates the root and scans each configured directory, starting with
local and then other profiles alphabetically. It shows empty profiles and uses
the same non-recursive discovery and waiver rules as the selector. Listing other
profiles does not expose them to the current shell's selector or change selection.

The `*` marker follows standard kubeconfig loading: `KUBECONFIG` file precedence,
or `~/.kube/config` if the variable is unset or empty. It matches the winning
context definition's absolute source path (without resolving symlinks) as well
as its name, so duplicate names do not highlight unrelated contexts. This global
selection is the stored context used by a standard client without explicit
overrides; it can differ from the KCM
selection (`>`). A saved current-context in an inactive file is not marked.
If the effective context is outside the discovered directories, or KUBECONFIG is
`/dev/null`, no global row is marked. Invalid external global config produces a
warning while still displaying the discovered inventory. Invalid discovered
configs fail listing with an error identifying the profile.

The global-config loader does not perform legacy file migration, authenticate,
or execute credential helpers. All markers remain plain text for redirected output.
