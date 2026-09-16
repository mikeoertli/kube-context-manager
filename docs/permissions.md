# Live permission checks

Permission checks run only when you call `kcm permissions` or `kcm can-i`.
KCM never caches permission results or discovery data, and does not query
permissions when listing contexts, opening a picker, initializing a shell,
or rendering a prompt.

Both commands use the inspected context's credentials and source file, regardless
of that file's `current-context`. They do not switch the shell's selection, edit
kubeconfigs, or renew its timeout. The cluster must be reachable; credential
helpers may ask you to log in. No kubectl executable is needed for these checks.

## Summarize permissions

```sh
kcm permissions                          # selected context and its namespace
kcm permissions -n app                   # another namespace
kcm permissions customer-a -n app        # another context in this profile
kcm permissions customer-a --file ~/.kube/prod/customer.yaml
kcm permissions --details                # full rule status, following the scope header
```

The header identifies the context, namespace, source file, and check time.
An omitted namespace uses the inspected context's configured namespace, or
`default` if none is set. Named contexts must be available in the current profile;
`--file` disambiguates duplicate names and does not bypass profile boundaries.

The compact table shows resource/API group, allowed verbs, and resource-name
restrictions. Non-resource URL permissions appear separately. Verbs are combined
only when the resource and its name restrictions match. Wildcards remain explicit;
KCM does not assign broad labels such as “admin” or “read-only.” `--details` shows
the entire returned status as formatted JSON, including resource and URL rules,
restrictions, the incomplete flag, and any evaluation error.

Kubernetes' [SelfSubjectRulesReview API](https://kubernetes.io/docs/reference/kubernetes-api/definitions/self-subject-rules-review-v1-authorization/)
returns rules for one namespace. Some authorizers cannot enumerate all rules.
KCM marks incomplete results prominently: a missing rule then means **unknown**,
not denied. Use `can-i` to ask about a specific action. A complete summary is still
only a snapshot of the reported rules in the stated namespace at check time.

## Check one action

```sh
kcm can-i list pods -n app
kcm can-i delete deployments.apps --context customer-a -n app
kcm can-i get pods/my-pod --subresource log -n app
kcm can-i create pods/my-pod --subresource exec -n app
kcm can-i list nodes
kcm can-i list pods --all-namespaces
```

Syntax: `kcm can-i VERB RESOURCE[.GROUP][/NAME]`.

- `--context NAME` inspects another context in this profile; `--file` resolves duplicates.
- `-n` / `--namespace` selects a namespace. `-A` / `--all-namespaces` checks across all namespaces; these flags cannot be combined.
- Live discovery resolves plural names, singular names, and short names, and detects cluster-scoped resources such as nodes. Those resources use cluster scope even when the context has a namespace.
- Core resources take precedence for unqualified names. If multiple extension groups match, qualify the group, such as `deployments.apps`.
- `--subresource` is separate from `/NAME`: `pods/my-pod --subresource log` checks logs for that pod.
- Use a concrete resource. Wildcard checks, non-resource URL checks, and parsing whole kubectl/Helm commands are not supported by `can-i`; URL and wildcard rules remain visible in `permissions`.

The [SelfSubjectAccessReview API](https://kubernetes.io/docs/reference/kubernetes-api/definitions/self-subject-access-review-v1-authorization/)
evaluates the action without performing it. Standard output is `yes` or `no`;
server explanations go to standard error.

| Exit code | Meaning |
| --- | --- |
| `0` | `yes`: action allowed by the authorization check |
| `1` | `no`: action not allowed by the authorization check |
| `2` | Invalid request, discovery/authentication/network failure, or inconclusive evaluation; no yes/no result |

An allowed result does not guarantee that an operation will succeed: admission
policies and other execution requirements still apply. The check does not execute
the action or analyze every API call a compound CLI command might make.

Both commands accept a positive `--request-timeout` duration (default `10s`):

```sh
kcm permissions --request-timeout 20s
```

Failures do not produce a cached fallback or a fabricated denial.
