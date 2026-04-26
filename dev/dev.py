#!/usr/bin/env python3
"""dev-env - Development Environment Manager (v4.2.0)

Usage:
    python3 dev/dev.py              # Dashboard
    python3 dev/dev.py start        # Start environment
    python3 -m dev                  # Same via package

Modules:
    cli.py        Argument parsing and dispatch
    core.py       Paths, UI, Docker helpers
    ipam.py       IP address management
    commands.py   All subcommand implementations
"""
import sys
from pathlib import Path

# Ensure project root is on sys.path so 'dev' package is importable
_ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(_ROOT))

from dev.cli import main  # noqa: E402

sys.exit(main())
