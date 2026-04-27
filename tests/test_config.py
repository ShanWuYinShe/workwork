"""Tests for layered configuration system."""

import pytest
from pathlib import Path
from dev.config import _deep_merge, _env_overrides_to_nested, _load_yaml, _set_nested


class TestDeepMerge:
    def test_flat_merge(self):
        base = {"a": 1, "b": 2}
        override = {"b": 3, "c": 4}
        result = _deep_merge(base, override)
        assert result == {"a": 1, "b": 3, "c": 4}

    def test_nested_merge(self):
        base = {"db": {"host": "localhost", "port": 5432}}
        override = {"db": {"port": 5433}}
        result = _deep_merge(base, override)
        assert result == {"db": {"host": "localhost", "port": 5433}}

    def test_override_wins(self):
        base = {"x": "old"}
        override = {"x": "new"}
        assert _deep_merge(base, override)["x"] == "new"


class TestSetNested:
    def test_single_key(self):
        d = {}
        _set_nested(d, "key", "val")
        assert d == {"key": "val"}

    def test_nested_path(self):
        d = {}
        _set_nested(d, "services.postgres.port", 5433)
        assert d == {"services": {"postgres": {"port": 5433}}}

    def test_numeric_coercion(self):
        d = {}
        _set_nested(d, "port", "5433")
        assert d["port"] == 5433


class TestEnvOverrides:
    def test_postgres_port(self):
        env = {"POSTGRES_PORT": "5433"}
        result = _env_overrides_to_nested(env)
        assert result["services"]["postgres"]["port"] == 5433

    def test_compose_project(self):
        env = {"COMPOSE_PROJECT": "myproject"}
        result = _env_overrides_to_nested(env)
        assert result["project"] == "myproject"

    def test_empty_env(self):
        assert _env_overrides_to_nested({}) == {}


class TestLoadYAML:
    def test_valid_yaml(self, tmp_path):
        f = tmp_path / "test.yml"
        f.write_text("project: test\nservices:\n  postgres:\n    port: 5433\n")
        result = _load_yaml(f)
        assert result["project"] == "test"
        assert result["services"]["postgres"]["port"] == 5433

    def test_missing_file(self, tmp_path):
        result = _load_yaml(tmp_path / "nonexistent.yml")
        assert result == {}

    def test_empty_file(self, tmp_path):
        f = tmp_path / "empty.yml"
        f.write_text("")
        assert _load_yaml(f) == {}
