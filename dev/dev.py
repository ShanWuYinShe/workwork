#!/usr/bin/env python3
"""dev-env - Development Environment Manager (v5.0.0)

Usage:
    python3 dev/dev.py              # Dashboard
    python3 dev/dev.py up           # Start environment
    python3 dev/dev.py down         # Stop environment
    python3 dev/dev.py --help       # Show help

Modules:
    cli.py        Argument parsing and dispatch
    core.py       Paths, UI, Docker helpers
    config.py     Layered YAML + .env configuration
    ipam.py       IP address management
    cmds/         Subcommand implementations by domain
"""
import sys
from pathlib import Path

_ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(_ROOT))

from dev.cli import main

sys.exit(main())
