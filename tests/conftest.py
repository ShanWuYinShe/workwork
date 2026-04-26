"""pytest configuration and fixtures."""

import sys
from pathlib import Path

# Ensure project root is on sys.path so dev package is importable
PROJECT_ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(PROJECT_ROOT))
