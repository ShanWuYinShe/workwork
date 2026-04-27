# dev-env

Docker 开发环境管理器，基于 Python CLI + Docker Compose + IPvlan 网络。

## 快速开始

```bash
# 1. 创建 Docker 网络（首次，需 sudo）
sudo bash setup-networks.sh

# 2. 初始化环境
python3 dev/dev.py init

# 3. 启动所有服务
python3 dev/dev.py start

# 4. 进入开发容器
python3 dev/dev.py shell

# 5. 查看状态
python3 dev/dev.py status
```

## 架构概览

```
service/                         # 项目根目录
├── dev.yml                      # 主配置文件（提交到 Git）
├── .env                         # 本地配置覆盖（不提交）
├── dev.docker-compose.yml       # 开发环境 Docker Compose
├── docker-compose.yml           # Xray 代理服务（生产）
├── instances.yaml               # Xray 实例定义
├── setup-networks.sh            # 创建 IPvlan 网络
├── pyproject.toml               # Python 项目定义
│
├── dev/                         # CLI 工具代码
│   ├── dev.py                   # 入口点
│   ├── cli.py                   # 参数解析与命令分发
│   ├── core.py                  # UI、环境加载、Docker 辅助函数
│   ├── config.py                # 分层配置系统
│   ├── ipam.py                  # IP 地址管理
│   ├── paths.py                 # 路径常量
│   ├── logging.yml              # 日志限制 overlay
│   ├── dev-test.yml             # 测试环境 Docker Compose
│   ├── Dockerfile               # 开发容器镜像
│   ├── Makefile                 # Make 快捷方式
│   └── cmds/                    # 命令实现
│       ├── lifecycle.py         # start, stop, teardown, restart, reset, build
│       ├── access.py            # shell, exec, run, logs, wait
│       ├── backup.py            # backup, restore, list-backups, snapshot
│       ├── system.py            # doctor, prune, version, completion, init, status, dashboard
│       ├── config_cmd.py        # config show/edit/ip
│       ├── ip_cmd.py            # ip list/alloc/free/used
│       ├── xray.py              # generate, list-instances, xray-start, xray-stop
│       └── test_env.py          # start-test, stop-test, reset-test, test
│
├── scripts/
│   └── generate_instances.py    # Xray 配置生成器
├── templates/                   # Xray/Nginx 配置模板
├── configs/                     # 生成的 Xray/Nginx 配置（含密钥，不提交）
├── cert/                        # TLS 证书（不提交）
├── nginx/                       # Nginx 反向代理
├── tests/                       # 单元测试
└── .local/                      # 本地数据（不提交）
    ├── ip-allocations.json      # IP 分配记录
    ├── instances.json           # Xray 客户端信息
    ├── backups/                 # 数据库备份
    └── snapshots/               # PostgreSQL 快照
```

## 服务组成

### 开发环境（dev.docker-compose.yml）

| 服务 | 镜像 | 端口 | 用途 |
|------|------|------|------|
| postgres | postgres:16-alpine | 5432→5433 | 主数据库 |
| redis | redis:7-alpine | 6379→6379 | 缓存 |
| minio | minio/minio:latest | 9000→9002, 9001→9003 | 对象存储 |
| dev | 自定义 (Node 22 + Go + Bun) | 3000, 3001, 8080 | 开发容器 |

所有服务通过 IPvlan L3 网络互联，每个服务分配独立 IP。

### 代理服务（docker-compose.yml）

| 服务 | 镜像 | 用途 |
|------|------|------|
| xray-reality-* | teddysun/xray | VLESS Reality 代理 |
| nginx-xhttp-* | nginx:alpine | TLS 终结 + XHTTP/WS 反代 |

Xray 支持 Reality（TCP 443）、XHTTP（8443）、WebSocket（8444）三种入站协议。

### 测试环境（dev/dev-test.yml）

独立桥接网络，使用固定端口和凭据，用于 CI 或集成测试。

| 服务 | 端口 | 凭据 |
|------|------|------|
| postgres | 5434 | test/test123 |
| redis | 6380 | - |
| minio | 9004/9005 | testminio/testminio123 |

## 命令参考

### 入口

```bash
python3 dev/dev.py <command> [options]
# 或通过 Make：
make <target> [VARIABLE=value]
```

运行不带参数显示交互式 dashboard。

### 全局选项

| 选项 | 说明 |
|------|------|
| `--quiet`, `-q` | 静默模式，只显示错误 |
| `--json` | JSON 格式输出（status 命令） |
| `--config <path>` | 指定配置文件路径 |

### 环境生命周期

| 命令 | 别名 | 说明 |
|------|------|------|
| `start` | `up` | 启动环境（分配 IP、健康检查） |
| `stop [--all]` | `down` | 停止环境（`--all` 停止所有项目） |
| `teardown [--force]` | - | 销毁环境及所有数据（需确认，`--force` 跳过） |
| `restart [service]` | - | 重启服务（默认全部） |
| `reset` | - | 清空所有服务数据 |
| `build` | - | 重建开发容器镜像 |

### 容器访问

| 命令 | 说明 |
|------|------|
| `shell [shell]` | 进入开发容器（默认 fish） |
| `exec <cmd...>` | 在容器内运行命令（交互式） |
| `run <cmd...>` | 在容器内运行命令（非交互） |
| `logs [service] [tail]` | 查看服务日志（默认跟踪 50 行） |
| `wait [--timeout N]` | 等待所有服务健康（默认 60s） |
| `status` / `ps` | 显示环境状态 |

### 备份与快照

| 命令 | 说明 |
|------|------|
| `backup` | 备份 PostgreSQL + Redis |
| `restore [target]` | 恢复备份（默认最新） |
| `list-backups` | 列出所有备份 |
| `snapshot list` | 列出 PostgreSQL 快照 |
| `snapshot create [name]` | 创建快照 |
| `snapshot restore [name]` | 恢复快照（`latest` 恢复最新） |
| `snapshot delete <name>` | 删除快照 |

### Xray 代理

| 命令 | 说明 |
|------|------|
| `generate` | 从模板生成 Xray + Nginx 配置 |
| `list-instances` | 列出已配置的 Xray 实例 |
| `xray-start` | 启动 Xray 代理服务 |
| `xray-stop` | 停止 Xray 代理服务 |

### 测试环境

| 命令 | 说明 |
|------|------|
| `start-test` | 启动隔离测试环境 |
| `stop-test` | 停止并销毁测试环境 |
| `reset-test` | 重置测试数据 |
| `test` | 完整重启（stop + start） |

### 系统维护

| 命令 | 说明 |
|------|------|
| `init` | 初始化环境检查 |
| `doctor` | 环境诊断（依赖、配置、端口、服务） |
| `prune` | 清理孤儿 Docker 资源 |
| `version` | 显示版本号 |
| `completion <shell>` | 生成 shell 补全脚本（bash/zsh） |
| `port-usage [port]` | 查看端口占用情况 |
| `config show` | 显示当前配置 |
| `config edit` | 编辑配置文件 |
| `config ip` | 显示 IP 分配 |
| `ip list` | 列出 IP 分配详情 |
| `ip alloc <service> [ip]` | 手动分配 IP |
| `ip free <service>` | 释放 IP |
| `ip used` | 显示网络中正在使用的 IP |

## 配置系统

采用分层配置：`dev.yml`（提交） → `.env`（本地覆盖） → 环境变量。

### dev.yml 结构

```yaml
project: service                    # Docker Compose 项目名

services:
  postgres:
    image: postgres:16-alpine
    port: 5433                      # 主机映射端口
    db: app                         # 数据库名
    user: app                       # 用户名
    password: app123                # 密码
  redis:
    image: redis:7-alpine
    port: 6379
  minio:
    image: minio/minio:latest
    api_port: 9002
    console_port: 9003
    user: minioadmin
    password: minioadmin123
  dev:
    ports: [3000, 3001, 8080]       # 开发容器暴露端口

network:
  name: ipvlan_docker               # Docker 网络名
  subnet: 172.18.0.0/16             # IPv4 子网
  ip_range: 172.18.0.0/24           # Docker 分配范围
  ipv6_subnet: "2605:...::/64"      # IPv6 子网
  ipv6_range: "2605:...:1::/80"
  reserved: "172.18.0.1-172.18.0.50" # 保留 IP 段
  alloc_start: "172.18.0.51"        # 自动分配起始 IP

xray:
  reality_dest: www.microsoft.com:443
  reality_server_names: [www.microsoft.com]
  default_short_ids: abcdef
```

### .env 覆盖

`.env` 文件中的变量会覆盖 `dev.yml` 中的对应值。映射关系：

| .env 变量 | 配置路径 |
|-----------|---------|
| `COMPOSE_PROJECT` | `project` |
| `POSTGRES_PORT` | `services.postgres.port` |
| `POSTGRES_DB` | `services.postgres.db` |
| `POSTGRES_USER` | `services.postgres.user` |
| `POSTGRES_PASSWORD` | `services.postgres.password` |
| `REDIS_PORT` | `services.redis.port` |
| `MINIO_USER` | `services.minio.user` |
| `MINIO_PASSWORD` | `services.minio.password` |
| `MINIO_API_PORT` | `services.minio.api_port` |
| `MINIO_CONSOLE_PORT` | `services.minio.console_port` |

## 网络架构

### IPvlan L3 网络

```
┌─────────────────────────────────────────────┐
│            ipvlan_docker (172.18.0.0/16)     │
│                                              │
│  .51  postgres     .52  redis                │
│  .53  minio        .54  dev                  │
│  ...  (xray/nginx 代理也在此网络)             │
│                                              │
│  保留段: 172.18.0.1 - 172.18.0.50            │
└─────────────────────────────────────────────┘
         │
    host eth0 (parent interface)
```

- IP 地址通过 IPAM 系统自动管理，持久化到 `.local/ip-allocations.json`
- 支持手动分配 (`ip alloc`)、释放 (`ip free`)、冲突检测
- `start` 时自动检测并解决 IP 冲突

### 代理流量路径

```
客户端 → [TCP 443] → Nginx (TLS 终结)
                    → Xray (VLESS Reality)
                    → Internet

客户端 → [TCP 443] → Nginx
                    → Xray (XHTTP/WS on :8443/:8444)
                    → Internet
```

## Xray 配置生成

`instances.yaml` 定义代理实例，`generate` 命令自动生成密钥和配置：

```bash
# 生成配置（自动创建 UUID 和密钥对）
python3 dev/dev.py generate

# 查看实例列表
python3 dev/dev.py list-instances
```

`instances.yaml` 格式：

```yaml
instances:
  - name: user1
    xray_name: xray-reality-user1   # 容器名
    path: user1                      # URL 路径前缀
    # client_id 和 private_key 留空自动生成
```

生成的文件：
- `configs/xray-reality-<name>.json` — Xray 配置
- `configs/nginx-xhttp-<name>.conf` — Nginx 反代配置
- `.local/instances.json` — 客户端连接信息（密钥，不提交）

## 开发容器

`dev/Dockerfile` 构建的开发容器包含：

- **Node.js 22** + npm/pnpm/yarn/bun
- **Go** (从 Alpine 提取)
- **PostgreSQL 客户端** (psql)
- **Redis 客户端** (redis-cli)
- **fish shell** (默认)，配置了快捷别名：
  - `pg` — 连接 PostgreSQL
  - `rd` — 连接 Redis
  - `ws` — 切换到 /workspace
  - `ll` — ls -la

容器内通过环境变量访问服务：
- `POSTGRES_HOST` / `POSTGRES_PORT` / `POSTGRES_DB`
- `REDIS_HOST` / `REDIS_PORT`
- `MINIO_ENDPOINT` / `MINIO_USER` / `MINIO_PASSWORD`

## 测试

```bash
# 运行单元测试
python3 -m pytest tests/ -v

# 启动隔离测试环境
python3 dev/dev.py start-test

# 在测试环境运行集成测试后清理
python3 dev/dev.py stop-test
```

测试覆盖：
- `test_cli.py` — CLI 参数解析
- `test_config.py` — 分层配置系统
- `test_ipam.py` — IP 地址转换与管理
- `test_generator.py` — Xray 配置解析
- `test_render.py` — 模板渲染

## 模块依赖关系

```
dev/dev.py          # 入口，设置 sys.path
  └── dev/cli.py    # argparse + 命令分发
        ├── dev/core.py    # UI, Docker 辅助, 环境加载
        │     ├── dev/paths.py     # 纯路径常量（无依赖）
        │     └── dev/config.py    # YAML + .env 配置
        │           └── dev/paths.py
        ├── dev/ipam.py    # IP 地址管理
        │     ├── dev/paths.py
        │     └── dev/config.py
        └── dev/cmds/*.py  # 各命令模块
              └── (core, config, ipam, paths)
```

`paths.py` 作为纯常量模块，打破了 `config.py ↔ core.py` 的循环依赖。

## Shell 补全

```bash
# Bash
eval "$(python3 dev/dev.py completion bash)"

# Zsh
source <(python3 dev/dev.py completion zsh)
```

## 常见操作

```bash
# 日常开发
python3 dev/dev.py start          # 启动环境
python3 dev/dev.py shell          # 进入容器
python3 dev/dev.py stop           # 下班停止

# 数据库操作
python3 dev/dev.py backup         # 备份
python3 dev/dev.py restore        # 恢复最新备份
python3 dev/dev.py snapshot create before-migration  # 创建快照

# 环境维护
python3 dev/dev.py doctor         # 诊断问题
python3 dev/dev.py prune          # 清理孤儿资源
python3 dev/dev.py port-usage     # 检查端口占用

# 代理管理
python3 dev/dev.py generate       # 重新生成代理配置
python3 dev/dev.py xray-start     # 启动代理

# 从零开始
python3 dev/dev.py teardown --force  # 完全销毁
python3 dev/dev.py start             # 重新创建
```
