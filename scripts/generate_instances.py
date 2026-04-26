#!/usr/bin/env python3
"""
Xray Instance Generator
Generates Xray Reality and Nginx configs from templates and instances.conf.
Auto-generates keys if not provided.

Usage:
    python3 scripts/generate-instances.py [--instances FILE]
"""

import sys
import json
import argparse
import subprocess
import uuid
from pathlib import Path

SCRIPT_DIR = Path(__file__).parent.resolve()
PROJECT_DIR = SCRIPT_DIR.parent
TEMPLATES_DIR = PROJECT_DIR / "templates"
CONFIGS_DIR = PROJECT_DIR / "configs"
CLIENT_INFO_FILE = PROJECT_DIR / ".local" / "instances.json"

INSTANCES_DEFAULT = PROJECT_DIR / "instances.conf"

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
    """Parse instances.conf and return list of instance dicts."""
    instances = []
    with open(path) as f:
        for line_num, line in enumerate(f, 1):
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            parts = [p.strip() for p in line.split(",")]
            if len(parts) < 3:
                print(f"  [WARN] line {line_num}: skipping, need at least 3 fields (name, xray_name, xray_path)")
                continue

            name = parts[0]
            xray_name = parts[1]
            xray_path = parts[2]
            client_id = parts[3] if len(parts) > 3 else ""
            private_key = parts[4] if len(parts) > 4 else ""
            short_ids = parts[5] if len(parts) > 5 and parts[5] else "abcdef"
            reality_dest = parts[6] if len(parts) > 6 and parts[6] else "www.microsoft.com:443"
            reality_server_names = parts[7] if len(parts) > 7 and parts[7] else '["www.microsoft.com"]'

            instances.append({
                "name": name,
                "xray_name": xray_name,
                "xray_path": xray_path,
                "client_id": client_id,
                "private_key": private_key,
                "short_ids": short_ids,
                "reality_dest": reality_dest,
                "reality_server_names": reality_server_names,
            })
    return instances


def render_template(template_path: Path, vars_: dict) -> str:
    """Render a template file, replacing {{VAR}} placeholders."""
    with open(template_path) as f:
        content = f.read()
    for key, val in vars_.items():
        content = content.replace(f"{{{{{key}}}}}", str(val))
    return content


def generate_instance(instance: dict, xray_template: str, nginx_template: str) -> dict:
    """Generate configs for one instance. Returns client info."""
    name = instance["name"]
    xray_name = instance["xray_name"]
    xray_path = instance["xray_path"]

    print(f"  Generating keys for {name}...")
    if not instance["client_id"]:
        instance["client_id"] = generate_uuid()
        print(f"    UUID: {instance['client_id']}")
    if not instance["private_key"]:
        instance["private_key"], instance["_public_key"] = generate_keypair()
        print(f"    Private key: {instance['private_key']}")
        print(f"    Public key:  {instance['_public_key']}")

    xray_vars = {
        "CLIENT_ID": instance["client_id"],
        "PRIVATE_KEY": instance["private_key"],
        "SHORT_IDS": f'["{instance["short_ids"]}"]',
        "XRAY_NAME": xray_name,
        "XRAY_PATH": xray_path,
        "REALITY_DEST": instance["reality_dest"],
        "REALITY_SERVER_NAMES": instance["reality_server_names"],
    }

    nginx_vars = {
        "NAME": name,
        "XRAY_NAME": xray_name,
        "XRAY_PATH": xray_path,
    }

    xray_config = render_template(xray_template, xray_vars)
    nginx_config = render_template(nginx_template, nginx_vars)

    xray_out = CONFIGS_DIR / f"xray-reality-{name}.json"
    nginx_out = CONFIGS_DIR / f"nginx-xhttp-{name}.conf"
    xray_out.write_text(xray_config)
    nginx_out.write_text(nginx_config)
    print(f"    xray config:   {xray_out.relative_to(PROJECT_DIR)}")
    print(f"    nginx config: {nginx_out.relative_to(PROJECT_DIR)}")

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
        instances_file = INSTANCES_DEFAULT

    if not instances_file.exists():
        print(f"ERROR: instances.conf not found at {instances_file}")
        sys.exit(1)

    CONFIGS_DIR.mkdir(parents=True, exist_ok=True)

    xray_template = TEMPLATES_DIR / "xray-reality-template.json"
    nginx_template = TEMPLATES_DIR / "nginx-xhttp-template.conf"

    if not xray_template.exists():
        print(f"ERROR: xray template not found: {xray_template}")
        sys.exit(1)
    if not nginx_template.exists():
        print(f"ERROR: nginx template not found: {nginx_template}")
        sys.exit(1)

    print(f"\n{'='*50}")
    print(f"Xray Instance Generator v4.1.0")
    print(f"{'='*50}")
    print(f"Reading instances: {instances_file}")
    print()

    instances = parse_instances(instances_file)
    if not instances:
        print("No instances found in instances.conf")
        sys.exit(1)

    client_info = []
    for inst in instances:
        print(f"[{inst['name']}]")
        ci = generate_instance(inst, xray_template, nginx_template)
        client_info.append(ci)
        print()

    CLIENT_INFO_FILE.parent.mkdir(parents=True, exist_ok=True)
    with open(CLIENT_INFO_FILE, "w") as f:
        json.dump(client_info, f, indent=2)
    print(f"  client info:     {CLIENT_INFO_FILE.relative_to(PROJECT_DIR)} (keep private!)")

    print()
    print(f"{'='*50}")
    print(f"Generation complete! {len(instances)} instance(s)")
    print()
    print("Next steps:")
    print("  1. Review generated configs in configs/")
    print("  2. Run: docker compose up -d")
    print("  3. Client info saved to .local/instances.json")
    print()
    print("To add a new instance:")
    print("  1. Add entry to instances.conf")
    print("  2. Run: python3 dev/dev.py generate")
    print()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Generate Xray instance configs")
    parser.add_argument("--instances", type=Path, help="Path to instances.conf")
    args = parser.parse_args()
    generate(args.instances)
