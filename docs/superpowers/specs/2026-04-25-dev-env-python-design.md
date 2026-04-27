# dev-env Python Rewrite Design

## 概述

将 `dev/dev.sh` (800+ 行 bash) 重写为 `dev/dev.py` (单文件 Python)，保持全部功能不变。

## 技术决策

| 决策 | 选择 |
|------|------|
| 入口 | `dev/dev.py`，单文件，零外部依赖 |
| CLI 框架 | argparse（Python 标准库） |
| 命令风格 | 简化：start/stop/restart/shell/start-test/stop-test/reset-test/generate/... |
| 标准库 | subprocess / pathlib / json / os / shutil / pathlib |
| 彩色输出 | ANSI 转义序列（标准库） |
| 配置文件 | .env（env 文件）、instances.conf（CSV）、templates/（保持不变） |

## 命令对照表

| Bash | Python (新) |
|------|-------------|
| `up` | `start` |
| `down` | `stop` |
| `teardown` | `teardown` |
| `restart` | `restart [service]` |
| `reset` | `reset` |
| `shell [shell]` | `shell [shell]` |
| `status` | `status` |
| `logs [svc]` | `logs [svc]` |
| `backup` | `backup` |
| `restore [name]` | `restore [name]` |
| `list-backups` | `list-backups` |
| `generate` | `generate` |
| `list-instances` | `list-instances` |
| `xray-up` | `xray-start` |
| `xray-down` | `xray-stop` |
| `test-up` | `start-test` |
| `test-down` | `stop-test` |
| `test-reset` | `reset-test` |
| `test` | `test` |
| `doctor` | `doctor` |
| `prune` | `prune` |
| `completion` | `completion` |
| `version` | `version` |

## 模块设计

### 常量与配置
```python
SCRIPT_DIR = Path(__file__).parent.resolve()
PROJECT_ROOT = SCRIPT_DIR.parent
COMPOSE_FILE = PROJECT_ROOT / "dev.docker-compose.yml"
TEST_COMPOSE_FILE = SCRIPT_DIR / "dev-test.yml"
BACKUP_DIR = PROJECT_ROOT / ".local" / "backups"
INSTANCES_FILE = PROJECT_ROOT / "instances.conf"
CONFIG_DIR = PROJECT_ROOT / "configs"
TEMPLATES_DIR = PROJECT_ROOT / "templates"
VERSION = "4.1.0"
```

### 环境变量加载
- 读取 `.env` 文件，`key=value` 格式
- 用 `pathlib.Path.read_text()` 读取
- 设置到 `os.environ` 供 subprocess 使用

### Docker 执行封装
```python
def run(*args, check=True, capture=False) -> CompletedProcess:
    """执行 docker compose 命令"""
    result = subprocess.run(
        ["docker", "compose", "-f", str(COMPOSE_FILE)] + list(args),
        capture_output=capture, text=True
    )
    if check and result.returncode != 0:
        die(f"Docker command failed: {' '.join(args)}")
    return result
```

### 健康检查
```python
def wait_healthy(container: str, name: str, timeout: int = 60) -> bool:
    """等待容器变为 healthy"""
    for _ in range(timeout):
        status = subprocess.run(
            ["docker", "inspect", "--format={{.State.Health.Status}}", container],
            capture_output=True, text=True
        )
        if status.stdout.strip() == "healthy":
            return True
        time.sleep(1)
    return False
```

### Xray 配置生成
- 复用现有 `scripts/generate-instances.py`（通过 subprocess 调用）
- 或迁移其核心逻辑到 `dev.py` 内（调用 xray 容器生成密钥）

### CLI 结构
```
dev.py
├── main()                   # 入口，argparse
├── 工具函数
│   ├── run_compose()
│   ├── load_env()
│   ├── wait_healthy()
│   └── is_container_running()
├── 命令函数
│   ├── cmd_start() / cmd_stop() / cmd_teardown()
│   ├── cmd_restart()
│   ├── cmd_reset()
│   ├── cmd_shell()
│   ├── cmd_status() / cmd_logs()
│   ├── cmd_backup() / cmd_restore() / cmd_list_backups()
│   ├── cmd_generate() / cmd_list_instances() / cmd_xray_start() / cmd_xray_stop()
│   ├── cmd_start_test() / cmd_stop_test() / cmd_reset_test() / cmd_test()
│   ├── cmd_doctor()
│   ├── cmd_prune()
│   ├── cmd_completion()
│   └── cmd_version()
└── 输出函数
    ├── log() / info() / warn() / error()
    └── die()
```

### 彩色输出
```python
RED = "\033[0;31m"
GREEN = "\033[0;32m"
YELLOW = "\033[1;33m"
BLUE = "\033[0;34m"
CYAN = "\033[0;36m"
BOLD = "\033[1m"
DIM = "\033[2m"
NC = "\033[0m"

def log(msg: str):  print(f"{GREEN}[DEV]{NC} {msg}")
def info(msg: str):  print(f"{BLUE}[INFO]{NC} {msg}")
def warn(msg: str):  print(f"{YELLOW}[WARN]{NC} {msg}")
def error(msg: str): print(f"{RED}[ERR]{NC} {msg}")
def die(msg: str):
    error(msg)
    sys.exit(1)
```

### argparse 配置
```python
parser = argparse.ArgumentParser(prog="dev.py", description="dev-env manager")
subparsers = parser.add_subparsers(dest="cmd", required=True)

# start
p = subparsers.add_parser("start", help="Start dev environment")
p.add_argument("--no-health-check", action="store_true")

# stop / teardown / reset / status / logs (with optional service)
# shell (with optional shell name)
# restart (with optional service)
# backup / restore (with optional name) / list-backups
# generate / list-instances / xray-start / xray-stop
# start-test / stop-test / reset-test / test
# doctor / prune / completion / version
```

## 错误处理

- `set -euo pipefail` 等价：`subprocess` 调用失败默认 die
- 特定场景允许软失败（如 backup 中某个服务失败，提示但不退出）
- SIGINT / SIGTERM 捕获，打印 "收到中断信号" 后优雅退出

## 测试策略

- 基础语法检查：`python3 -m py_compile dev.py`
- 命令路由测试：每个子命令至少触发一次 `--help`
- 端到端在服务器测试（复用现有测试流程）

## 文件变化

| 文件 | 操作 |
|------|------|
| `dev/dev.sh` | 删除 |
| `dev/dev.py` | 新增 |
| `dev/Dockerfile` | 保持（构建逻辑不变） |
| `dev/dev-test.yml` | 保持 |
| `dev/docker-compose.yml` | 保持 |
| `scripts/generate-instances.py` | 保持（可被 dev.py 调用） |
| `templates/` | 保持 |
| `instances.conf` | 保持 |

## 兼容性

- Python 版本：3.8+（Ubuntu 22.04 自带 3.10，macOS 自带 3.9）
- 无需安装任何 pip 包
- `python3 dev.py --help` 即用
- 可添加 shebang `#!/usr/bin/env python3` 使其直接可执行
