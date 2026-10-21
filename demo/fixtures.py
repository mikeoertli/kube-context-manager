"""Synthetic kubeconfigs for the disposable demo. No credentials or real hosts."""
import json
import os
import pathlib
import sys
import time

runtime = pathlib.Path(sys.argv[1])
root = runtime / "kube"
for profile in ["dev", "qa", "prod", "other"]:
    (root / profile).mkdir(parents=True, exist_ok=True)


def config(name, host):
    return {
        "apiVersion": "v1", "kind": "Config",
        "clusters": [{"name": name, "cluster": {"server": f"https://{host}.invalid"}}],
        "users": [{"name": "demo", "user": {}}],
        "contexts": [{"name": name, "context": {"cluster": name, "user": "demo", "namespace": "default"}}],
        "current-context": name,
    }


for file, name, host in [
    (root / "config", "local-lab", "local"),
    (root / "dev" / "config", "api-dev", "dev"),
    (root / "prod" / "config", "api-prod", "prod"),
    (root / "other" / "config", "sandbox", "sandbox"),
]:
    file.write_text(json.dumps(config(name, host), indent=2) + "\n")
downloads = runtime / "Downloads"
downloads.mkdir()
for name, age in [("older.kubeconfig", 7200), ("new-cluster.kubeconfig", 60)]:
    file = downloads / name
    file.write_text(json.dumps(config("arn:example:cluster/qa", "qa"), indent=2) + "\n")
    modified = time.time() - age
    os.utime(file, (modified, modified))
(root / "README.md").write_text("Demo documentation: intentionally ignored by discovery.\n")
(runtime / "clipboard.kubeconfig").write_text(json.dumps(config("clipboard-dev", "dev"), indent=2) + "\n")
# Keep demo clipboard imports isolated from the user's actual clipboard.
clipboard_bin = runtime / "clipboard-bin"
clipboard_bin.mkdir()
for name in ["pbpaste", "wl-paste", "xclip", "xsel"]:
    reader = clipboard_bin / name
    reader.write_text('#!/bin/sh\nexec /bin/cat "$KCM_DEMO_DIR/clipboard.kubeconfig"\n')
    reader.chmod(0o700)
(runtime / "settings.toml").write_text(
    f"kube_root = {json.dumps(str(root))}\n"
    'local_hosts = ["local.invalid"]\n'
    '[profiles.prod]\ntimeout = "6s"\n'
)
