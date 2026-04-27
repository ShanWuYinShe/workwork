#!/usr/bin/env python3
"""
Xray Instance Generator
Generates Xray Reality and Nginx configs from templates and instances.yaml.
Auto-generates keys if not provided.

Usage:
    python3 scripts/generate_instances.py [--instances FILE]
"""

import json
import sys
import subprocess
import uuid
import argparse
from pathlib import Path

import yaml

# Make dev package importable when run as a standalone script
sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from dev.paths import TEMPLATES_DIR, CONFIG_DIR, INSTANCES_JSON, INSTANCES_CONF
from dev.config import get_config


def run_xray(cmd: list[str]) -> str:
    """Run xray command in a container and return output."""
    result = subprocess.run(
        ["docker", "run", "--rm", "--network=none",
         "-i", "teddysun/xray"] + cmd,
        capture_output=True, text=True, timeout=30
    )
    if result.returncode != 0:
        print(f"    [WARN] xray command failed: {result.stderr.strip()}")
    return result.stdout.strip()


def generate_uuid() -> str:
    output = run_xray(["xray", "uuid"])
    if output:
        return output
    return str(uuid.uuid4())


def generate_keypair() -> tuple[str, str]:
    """Generate X25519 key pair. Returns (private_key, public_key)."""
    output = run_xray(["xray", "x25519"])
    private_key = ""
    public_key = ""
    for line in output.splitlines():
        if "PrivateKey:" in line or "Private key:" in line:
            private_key = line.split(":", 1)[1].strip()
        elif "Password (PublicKey):" in line or "Public key:" in line:
            public_key = line.split(":", 1)[1].strip()
    if not private_key or not public_key:
        print("    [WARN] xray x25519 failed, using openssl")
        result = subprocess.run(
            ["openssl", "rand", "-base64", "32"],
            capture_output=True, text=True, timeout=10
        )
        private_key = result.stdout.strip()
        public_key = "[auto-generated-openssl]"
    return private_key, public_key


def parse_instances(path: Path) -> list[dict]:
    """Parse instances.yaml and return list of instance dicts."""
    if not path.exists():
        print(f"ERROR: instances file not found: {path}")
        sys.exit(1)

    with open(path) as f:
        data = yaml.safe_load(f)

    if not data or "instances" not in data:
        print("No instances found in file")
        return []

    defaults = get_config("xray", {})

    instances = []
    for inst in data["instances"]:
        if not inst.get("name"):
            continue
        instances.append({
            "name": inst["name"],
            "xray_name": inst.get("xray_name", f"xray-reality-{inst['name']}"),
            "xray_path": inst.get("path", inst["name"]),
            "client_id": inst.get("client_id", ""),
            "private_key": inst.get("private_key", ""),
            "short_ids": inst.get("short_ids", defaults.get("default_short_ids", "abcdef")),
            "reality_dest": inst.get("reality_dest", defaults.get("reality_dest", "www.microsoft.com:443")),
            "reality_server_names": inst.get("reality_server_names",
                                             defaults.get("reality_server_names", ["www.microsoft.com"])),
        })
    return instances


def render_template(template_path: Path, vars_: dict) -> str:
    """Render a template file, replacing {{VAR}} placeholders."""
    with open(template_path) as f:
        content = f.read()
    for key, val in vars_.items():
        if isinstance(val, list):
            val = json.dumps(val)
        content = content.replace(f"{{{{{key}}}}}", str(val))
    return content


def _atomic_write(path: Path, content: str):
    """Write file atomically: write to hidden temp, then rename."""
    tmp = path.parent / f".{path.name}.tmp"
    tmp.write_text(content)
    tmp.rename(path)


def generate_instance(instance: dict, xray_template: str, nginx_template: str) -> dict:
    """Generate configs for one instance. Returns client info."""
    name = instance["name"]
    xray_name = instance["xray_name"]
    xray_path = instance["xray_path"]
    project_dir = INSTANCES_CONF.parent

    print(f"  Generating keys for {name}...")
    if not instance["client_id"]:
        instance["client_id"] = generate_uuid()
        print(f"    UUID: {instance['client_id']}")
    if not instance["private_key"]:
        instance["private_key"], instance["_public_key"] = generate_keypair()
        print(f"    Private key: {instance['private_key']}")
        print(f"    Public key:  {instance['_public_key']}")

    short_ids = instance["short_ids"]
    if isinstance(short_ids, str):
        short_ids = [short_ids]

    server_names = instance["reality_server_names"]
    if isinstance(server_names, str):
        server_names = [s.strip() for s in server_names.split(",")]

    xray_vars = {
        "CLIENT_ID": instance["client_id"],
        "PRIVATE_KEY": instance["private_key"],
        "SHORT_IDS": json.dumps(short_ids),
        "XRAY_NAME": xray_name,
        "XRAY_PATH": xray_path,
        "REALITY_DEST": instance["reality_dest"],
        "REALITY_SERVER_NAMES": json.dumps(server_names),
    }

    nginx_vars = {
        "NAME": name,
        "XRAY_NAME": xray_name,
        "XRAY_PATH": xray_path,
    }

    xray_config = render_template(xray_template, xray_vars)
    nginx_config = render_template(nginx_template, nginx_vars)

    xray_out = CONFIG_DIR / f"xray-reality-{name}.json"
    nginx_out = CONFIG_DIR / f"nginx-xhttp-{name}.conf"
    _atomic_write(xray_out, xray_config)
    _atomic_write(nginx_out, nginx_config)
    print(f"    xray config:   {xray_out.relative_to(project_dir)}")
    print(f"    nginx config:  {nginx_out.relative_to(project_dir)}")

    return {
        "name": name,
        "xray_name": xray_name,
        "client_id": instance["client_id"],
        "private_key": instance["private_key"],
        "public_key": instance.get("_public_key", ""),
        "xray_path": xray_path,
        "reality_dest": instance["reality_dest"],
    }


def generate(instances_file: Path = None):
    if instances_file is None:
        instances_file = INSTANCES_CONF

    CONFIG_DIR.mkdir(parents=True, exist_ok=True)

    xray_template = TEMPLATES_DIR / "xray-reality-template.json"
    nginx_template = TEMPLATES_DIR / "nginx-xhttp-template.conf"

    if not xray_template.exists():
        print(f"ERROR: xray template not found: {xray_template}")
        sys.exit(1)
    if not nginx_template.exists():
        print(f"ERROR: nginx template not found: {nginx_template}")
        sys.exit(1)

    print(f"\n{'='*50}")
    print(f"Xray Instance Generator v5.0.0")
    print(f"{'='*50}")
    print(f"Reading instances: {instances_file}")
    print()

    instances = parse_instances(instances_file)
    if not instances:
        print("No instances found")
        sys.exit(1)

    client_info = []
    for inst in instances:
        print(f"[{inst['name']}]")
        ci = generate_instance(inst, xray_template, nginx_template)
        client_info.append(ci)
        print()

    INSTANCES_JSON.parent.mkdir(parents=True, exist_ok=True)
    _atomic_write(INSTANCES_JSON, json.dumps(client_info, indent=2) + "\n")

    project_dir = INSTANCES_CONF.parent
    print(f"  client info:     {INSTANCES_JSON.relative_to(project_dir)} (keep private!)")

    print()
    print(f"{'='*50}")
    print(f"Generation complete! {len(instances)} instance(s)")
    print()
    print("Next steps:")
    print("  1. Review generated configs in configs/")
    print("  2. Run: docker compose up -d")
    print("  3. Client info saved to .local/instances.json")
    print()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Generate Xray instance configs")
    parser.add_argument("--instances", type=Path, help="Path to instances.yaml")
    args = parser.parse_args()
    generate(args.instances)
