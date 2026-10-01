"""Synthetic kubeconfigs for the disposable demo. No credentials or real hosts."""
import json
import pathlib
import sys

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
    (runtime / "incoming.yaml", "downloaded-context", "qa"),
]:
    file.write_text(json.dumps(config(name, host), indent=2) + "\n")
(root / "README.md").write_text("Demo documentation: intentionally ignored by discovery.\n")
(runtime / "settings.toml").write_text(
    f"kube_root = {json.dumps(str(root))}\n"
    'local_hosts = ["local.invalid"]\n'
    '[profiles.prod]\ntimeout = "6s"\n'
)
