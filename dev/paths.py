"""Path constants and version. No dependencies — safe to import from anywhere."""

from pathlib import Path

VERSION = "5.0.0"

SCRIPT_DIR = Path(__file__).resolve().parent
PROJECT_ROOT = SCRIPT_DIR.parent

COMPOSE_FILE = PROJECT_ROOT / "dev.docker-compose.yml"
TEST_COMPOSE_FILE = SCRIPT_DIR / "dev-test.yml"
LOGGING_OVERLAY = SCRIPT_DIR / "logging.yml"
BACKUP_DIR = PROJECT_ROOT / ".local" / "backups"
SNAPSHOT_DIR = PROJECT_ROOT / ".local" / "snapshots"
INSTANCES_CONF = PROJECT_ROOT / "instances.yaml"
INSTANCES_JSON = PROJECT_ROOT / ".local" / "instances.json"
CONFIG_DIR = PROJECT_ROOT / "configs"
TEMPLATES_DIR = PROJECT_ROOT / "templates"
IP_ALLOC_FILE = PROJECT_ROOT / ".local" / "ip-allocations.json"
ENV_FILE = PROJECT_ROOT / ".env"
CONFIG_FILE = PROJECT_ROOT / "dev.yml"
GENERATOR_SCRIPT = PROJECT_ROOT / "scripts" / "generate_instances.py"
CERT_DIR = PROJECT_ROOT / "cert"
