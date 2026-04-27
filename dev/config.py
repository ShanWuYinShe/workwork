"""Layered configuration: dev.yml (committed) + .env (local overrides)."""

import os
from pathlib import Path
from typing import Any

import yaml

from dev.paths import PROJECT_ROOT, CONFIG_FILE, ENV_FILE

_config: dict = {}


def _deep_merge(base: dict, override: dict) -> dict:
    """Recursively merge override into base (override wins)."""
    result = base.copy()
    for key, val in override.items():
        if key in result and isinstance(result[key], dict) and isinstance(val, dict):
            result[key] = _deep_merge(result[key], val)
        else:
            result[key] = val
    return result


def _load_yaml(path: Path) -> dict:
    if not path.exists():
        return {}
    text = path.read_text()
    if not text.strip():
        return {}
    return yaml.safe_load(text) or {}


def _load_dotenv(path: Path) -> dict:
    """Parse .env into flat key-value dict."""
    env = {}
    if not path.exists():
        return env
    for line in path.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, _, value = line.partition("=")
        env[key.strip()] = value.strip()
    return env


def _env_overrides_to_nested(env: dict) -> dict:
    """Convert flat env vars to nested config overrides."""
    mapping = {
        "COMPOSE_PROJECT": "project",
        "POSTGRES_DB": "services.postgres.db",
        "POSTGRES_USER": "services.postgres.user",
        "POSTGRES_PASSWORD": "services.postgres.password",
        "POSTGRES_PORT": "services.postgres.port",
        "REDIS_PORT": "services.redis.port",
        "MINIO_USER": "services.minio.user",
        "MINIO_PASSWORD": "services.minio.password",
        "MINIO_API_PORT": "services.minio.api_port",
        "MINIO_CONSOLE_PORT": "services.minio.console_port",
        "IPVLAN_RESERVED": "network.reserved",
    }
    overrides: dict = {}
    for env_key, dot_path in mapping.items():
        if env_key in env:
            _set_nested(overrides, dot_path, env[env_key])
    return overrides


def _set_nested(d: dict, dot_path: str, value: Any):
    """Set a value at a dot-separated path in nested dict."""
    keys = dot_path.split(".")
    for k in keys[:-1]:
        d = d.setdefault(k, {})
    if isinstance(value, str) and value.isdigit():
        value = int(value)
    d[keys[-1]] = value


def load_config(config_path: Path = None) -> dict:
    """Load and merge configuration from dev.yml + .env + os.environ."""
    global _config
    path = config_path or CONFIG_FILE

    base = _load_yaml(path)
    dotenv = _load_dotenv(ENV_FILE)
    env_overrides = _env_overrides_to_nested({**dotenv, **{k: v for k, v in os.environ.items() if v}})

    _config = _deep_merge(base, env_overrides)
    return _config


def get_config(key: str, default: Any = None) -> Any:
    """Get config value by dot-separated path. E.g. get_config('services.postgres.port')."""
    if not _config:
        load_config()
    keys = key.split(".")
    val = _config
    for k in keys:
        if isinstance(val, dict):
            val = val.get(k)
        else:
            return default
        if val is None:
            return default
    return val


def get_all_config() -> dict:
    """Return the full merged config dict."""
    if not _config:
        load_config()
    return _config


def validate_config() -> list[str]:
    """Validate config values. Returns list of issues (empty = valid)."""
    if not _config:
        load_config()
    issues = []

    if not get_config("project"):
        issues.append("project name is required")
    for svc in ["postgres", "redis", "minio"]:
        if not get_config(f"services.{svc}.image"):
            issues.append(f"services.{svc}.image is required")

    for svc in ["postgres", "redis"]:
        port = get_config(f"services.{svc}.port")
        if port and not (1 <= int(port) <= 65535):
            issues.append(f"services.{svc}.port ({port}) out of range")

    subnet = get_config("network.subnet", "")
    if subnet and "/" not in subnet:
        issues.append(f"network.subnet ({subnet}) must include CIDR mask")

    return issues
