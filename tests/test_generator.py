"""Tests for Xray config generator."""

from pathlib import Path
from scripts.generate_instances import parse_instances, render_template


def test_parse_instances_empty(tmp_path):
    """Empty file yields no instances."""
    f = tmp_path / "instances.yaml"
    f.write_text("instances: []\n")
    assert parse_instances(f) == []


def test_parse_instances_minimal(tmp_path):
    """Minimal instance with only required fields."""
    f = tmp_path / "instances.yaml"
    f.write_text("instances:\n  - name: alice\n    xray_name: xray-alice\n    path: alice\n")
    instances = parse_instances(f)
    assert len(instances) == 1
    inst = instances[0]
    assert inst["name"] == "alice"
    assert inst["xray_name"] == "xray-alice"
    assert inst["xray_path"] == "alice"
    assert inst["client_id"] == ""
    assert inst["private_key"] == ""


def test_parse_instances_full(tmp_path):
    """Full instance with all fields."""
    f = tmp_path / "instances.yaml"
    f.write_text(
        "instances:\n"
        "  - name: bob\n"
        "    xray_name: xray-bob\n"
        "    path: bobpath\n"
        "    client_id: uuid-123\n"
        "    private_key: privkey-456\n"
        "    short_ids: abc123\n"
        "    reality_dest: example.com:443\n"
        "    reality_server_names:\n"
        "      - example.com\n"
    )
    instances = parse_instances(f)
    assert len(instances) == 1
    inst = instances[0]
    assert inst["name"] == "bob"
    assert inst["client_id"] == "uuid-123"
    assert inst["private_key"] == "privkey-456"
    assert inst["short_ids"] == "abc123"
    assert inst["reality_dest"] == "example.com:443"
    assert inst["reality_server_names"] == ["example.com"]


def test_parse_instances_multiple(tmp_path):
    f = tmp_path / "instances.yaml"
    f.write_text("instances:\n  - name: a\n    xray_name: x-a\n    path: a\n  - name: b\n    xray_name: x-b\n    path: b\n")
    instances = parse_instances(f)
    assert len(instances) == 2
    assert instances[0]["name"] == "a"
    assert instances[1]["name"] == "b"


def test_parse_instances_defaults(tmp_path):
    """Missing optional fields get defaults from dev.yml."""
    f = tmp_path / "instances.yaml"
    f.write_text("instances:\n  - name: carol\n    xray_name: x-carol\n    path: carol\n")
    instances = parse_instances(f)
    inst = instances[0]
    assert inst["short_ids"] == "abcdef"
    assert "www.microsoft.com" in inst["reality_dest"]
