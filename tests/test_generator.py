"""Tests for Xray config generator."""

from pathlib import Path
from scripts.generate_instances import parse_instances


def test_parse_instances_empty(tmp_path):
    """Empty or comment-only files yield no instances."""
    f = tmp_path / "instances.conf"
    f.write_text("# comment\n")
    assert parse_instances(f) == []


def test_parse_instances_minimal(tmp_path):
    """Minimal 3-field line should parse with defaults."""
    f = tmp_path / "instances.conf"
    f.write_text("alice, xray-alice, alice\n")
    instances = parse_instances(f)
    assert len(instances) == 1
    inst = instances[0]
    assert inst["name"] == "alice"
    assert inst["xray_name"] == "xray-alice"
    assert inst["xray_path"] == "alice"
    assert inst["client_id"] == ""
    assert inst["private_key"] == ""


def test_parse_instances_full(tmp_path):
    """Full line with all fields."""
    f = tmp_path / "instances.conf"
    line = "bob, xray-bob, bobpath, uuid-123, privkey-456, abc123, example.com:443, [\"example.com\"]"
    f.write_text(line + "\n")
    instances = parse_instances(f)
    assert len(instances) == 1
    inst = instances[0]
    assert inst["name"] == "bob"
    assert inst["client_id"] == "uuid-123"
    assert inst["private_key"] == "privkey-456"
    assert inst["short_ids"] == "abc123"
    assert inst["reality_dest"] == "example.com:443"
    assert inst["reality_server_names"] == '["example.com"]'


def test_parse_instances_short_ids_default(tmp_path):
    """Missing short_ids gets default 'abcdef'."""
    f = tmp_path / "instances.conf"
    f.write_text("carol, xray-carol, carol, , , , ,\n")
    instances = parse_instances(f)
    assert instances[0]["short_ids"] == "abcdef"


def test_parse_instances_multiple(tmp_path):
    f = tmp_path / "instances.conf"
    f.write_text("a, x-a, a\nb, x-b, b\n")
    instances = parse_instances(f)
    assert len(instances) == 2
    assert instances[0]["name"] == "a"
    assert instances[1]["name"] == "b"


def test_parse_instances_skips_invalid(tmp_path):
    f = tmp_path / "instances.conf"
    f.write_text("# comment\na, x-a, a\n\nb\n")
    instances = parse_instances(f)
    assert len(instances) == 1  # 'b' has < 3 fields
