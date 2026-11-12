# workwork

Docker 代理基础设施：Xray VLESS XHTTP 代理 / Nginx 反代 / Go 管理面板。

通用约束（Git 规范、Commit 格式、验证流程、工具链偏好、Docker 等）以全局 `~/AGENTS.md` 为准（各 AI 工具的全局指令文件均链接到该文件），本文件只做项目特有补充，不得放宽或违背全局约束。

AI Agent 工作目录：所有 AI agent 工作期间产生的中间文件写入项目根 `.workwork/`（已 `.gitignore`）。注意与运行时数据目录 `.wework/`（服务配置、日志挂载源，同样 gitignored）区分：两者名字相近但用途无关，不要混淆。

## 硬约束

所有服务功能必须隔离在 Docker 容器内。 禁止修改宿主机环境设置（iptables、sysctl、网络配置），创建 Docker 网络除外。IPv4 路由走 Docker bridge NAT；IPv6 路由走 ipvlan L3。admin 服务经自建 docker API 白名单代理（compose `docker-proxy` 服务，method+path 级白名单见 `services/docker_proxy/policy.go`）和 `containerExec()` 操作其他容器——新增 docker API 调用时必须同步白名单与集成测试。

## 发布门禁（硬性）：CI 全绿再发布

发布前验证以 GitHub Actions 为准：PR 上 `admin` / `security-scan` / `govulncheck` / `gitleaks` / `image-build` 五个 job 全部通过即视为门禁通过。PR 未全绿不得合并；main 上的 CI 未绿不得部署。

- 不再要求本地构建镜像或起服务做发布前验证：`make build <受影响服务...>` / `make up <svc...>` 是本地排障与交互式调试手段，不是发布前置条件。
- 镜像可构建性由 CI 的 `image-build` job（`docker compose build`）在 PR 上验证；本仓没有 Release workflow，镜像在服务器部署时构建，CI 不推送镜像仓库。
- 本地 Docker 仅用于交互式调试；本地不具备 nginx/xray 启动条件（缺 `.env` / `cert/` / `.wework/` 与外部 `ipvlan_docker` 网络）时不在本地验证，也不要为了验证伪造线上证书或密钥。
- 服务器部署仍按 `workwork-deploy` skill：合并后在服务器执行 `make deploy-changed`（pull → 按变更最小 build/up → 部署后自校验：等 healthy + 核对版本指纹，不通过即失败）。发布 tag 按 `git-workflow` skill 的 tag 纪律打并推送。

## CI 门禁与本地验证分工

重活（全量测试、构建、打包、镜像构建）一律交给 GitHub Actions，本地只跑秒级检查。
完整流程见全局 skill `github-ci-flow`：开发分支推送 → `gh pr create` → `gh pr checks --watch` 全绿 →
`gh pr merge --rebase --delete-branch` → 本地 `git pull --ff-only` 并清理 worktree。

- 本地（提交前，秒级）：
  - `gofmt -l services/`（应无输出）；
  - 改脚本时 `bash -n <脚本>`（涉及 `scripts/`、`services/nginx`、`services/xray`、`setup-networks.sh`）；
  - 改编排/版本/环境变量点位时 `docker compose config -q`；
  - `git diff --check`（改动无行尾空白、无冲突标记）。
  `make check` 全量是分钟级重活（Go vet / test -race、shellcheck、版本一致性、env schema 正负例、前端 format/lint/check/test/build），本地不跑，由 CI 的 `Admin (Go + UI)` job 执行。
- CI 覆盖（`.github/workflows/ci.yml`，`push` / `pull_request` 目标均为 `main`，无 paths 过滤）：
  - `Admin (Go + UI)`：`make check` 全量 + `setup-networks.sh` 地址推导 dry-run + 部署路径映射覆盖（compose 服务 ↔ `services/` 目录 ↔ `deploy-changed.sh`）；
  - `Security scan (gosec, report-only)`：gosec 报告模式，不阻塞合并，发现只进 CI 日志供巡检；
  - `Vulnerability scan (govulncheck)`：`docker_proxy` 阻断；`admin` 命中须落在 `ci.yml` 的 `GOVULN_ALLOWED` allowlist 内，出现清单外新漏洞或工具自身运行失败即失败；
  - `Secret scan (gitleaks)`：全量 git 历史扫描，误报按 `.gitleaks.toml` / `.gitleaksignore` 豁免；
  - `Image build smoke`：`docker compose build` 构建四个服务镜像，把 Dockerfile/编排构建损坏提前到合并前。
  纯文档 PR 同样跑全套 job；PR 连续推送会取消旧 run（concurrency），main push 不取消。
- CI 覆盖不到、只能在服务器验证的：部署后自校验（`deploy-changed.sh` 等 healthy + 版本指纹核对，失败自动回滚）、线上真实网络行为（IPv6 ipvlan 路由、TLS 证书链、fail2ban 实际封禁）——按 `workwork-deploy` / `workwork-security` skill 在服务器执行。
- 发版/发布门禁：见「发布门禁（硬性）」——发布前以 PR 的 CI 全绿为准，本地不再 build/up。


## 架构速览

完整设计叙事（服务分工、网络拓扑、IPv6 寻址、RBAC 矩阵）见 [`docs/architecture.md`](./docs/architecture.md)，需要时再读，不要常驻记忆。本节只保留做错会出事的禁令：

- 不要让 xray 加入 `internal_docker`（原因见 docs）
- XHTTP 模式必须是 `packet-up`，不要改成 `stream-up`
- 不要为 IPv4 入站加其他转发方式（宿主 iptables DNAT 等）
- 改 `services/admin/configs/` 下模板 = rebuild admin（模板与二进制原子对应）；模板渲染出的运行时配置只在 xray/nginx 启动时读取，`deploy-changed.sh` 会按模板变更或渲染产物哈希变化 `restart` 对应服务——改该脚本时勿丢这条映射，否则新配置渲染了却静默不生效

## 关键约定

- 文档单一来源：架构"是什么"只写在 `docs/architecture.md`，AGENTS 不复述实现细节。文档与可验证实现冲突时以代码为准并修正文档，不要按错误描述继续。
- 配置字段核对: Xray、Nginx、Docker 等配置以官方源码/文档为准；优先读取官方 GitHub 仓库的 Go struct JSON tag 或官方文档确认字段名大小写、合法值范围和字段语义。
- Never use `RemoteAddr` for auth — 所有流量经 Docker bridge，看起来都是内网地址。
- Internal auth: `INTERNAL_TOKEN`（启动时自动生成，存储在 `.wework/.internal-token`）；fail2ban 使用独立 scoped token（`.wework/fail2ban/token`）。`INTERNAL_TOKEN` 默认全权（仓库代码不主动发起携带该 token 的调用，它服务于外部自动化，默认收紧会静默破坏它们）；配置 `INTERNAL_TOKEN_ALLOWED_PREFIXES`（逗号分隔路径前缀）后只豁免白名单内路径，白名单外的使用会以 `[security]` 留痕——新部署建议按日志逐步收敛。携带方式：`X-Internal-Token` 头或 `Authorization: Bearer <token>`（nginx 透传，外部自动化经公网可用）。token 会被渲染进 nginx map 做**真实值匹配**，字符集限 `[A-Za-z0-9._~-]`（含引号/`$`/空白会破坏配置，启动即拒绝）。持合法 token 的请求豁免全部防护层（nginx 值匹配豁免限速与 IP 封禁；admin 层恒时比较后豁免应用层限流与 IP 封禁）——授权请求不被防护误伤；伪造 token 在 nginx 就不匹配，照常限流/封禁。
- Secrets: gitignored `cert/`、`.wework/`。本地 `.env` 覆盖默认值。
- Xray 客户端归属: 登录用户是 nginx 认证身份，客户端是独立代理身份，一个用户可以拥有多个客户端。非 admin 只能管理自己名下的客户端；admin 可看/管全部，并可代指定用户创建（`owner` 字段）。客户端 `name`（网页上的客户端用户名）就是 Xray email；同一入站内必须唯一，留空时按 owner 自动生成 `owner`/`owner-2`。
- 配置文件写入：统一使用 `runtime.AtomicWriteFile`（写 .tmp → rename），防止写到一半崩溃导致配置损坏。禁止直接 `os.WriteFile` 写配置文件。
- 错误响应：业务错误用 `writeError(w, msg, code)`（msg 返回客户端）；内部错误（文件 I/O、Docker API、第三方 API 等可能泄露内部状态的）必须用 `writeInternalError(w, err)`（只返回固定 "Internal error"，真实错误记日志）。
- 新增 API 端点：在 `routes.go` 的 `apiRoutes` 表登记（method/path/handler/role/bodyLimitExempt）；认证端点 role 只允许 viewer/admin，公开端点必须进契约测试的 allowlist。handler 实现放对应 internal/ 域包（依赖单向无环）；大上传端点只需在路由表标 `bodyLimitExempt`，`main_test.go` 的契约测试会核对豁免集合。
- 审计留痕：变更类端点（增删改、启停、发布、封禁等）必须在成功路径记 `log.Printf("[security] <动作> <关键标识> by=%s", actor)`，actor 取 `requestctx.ActorFrom(r.Context())`；只读端点不需要。用户可控的标识（文件名、昵称等）必须 `%q` 转义——换行注入会伪造出假审计行。请求日志（RequestLogMiddleware）已带 user 字段兜底。
- 环境变量：通过 `platform.GetEnv()` 读取（见 `services/admin/internal/platform/env.go`），不直接用 `os.Getenv`；新增/修改变量必须同步 `scripts/env-schema.tsv`、compose 的 `${KEY:-...}` 兜底与 `.env.example`，`make check` 会拦漂移。


## skill 索引（改对应领域前先加载对应 skill）

| skill | 管什么 |
|---|---|
| `workwork-deploy` | 部署拓扑、服务器发布流程、`deploy-changed.sh` 与版本指纹核对 |
| `workwork-security` | 认证/RBAC、Internal token、fail2ban、代理与防护层约定 |
| `workwork-versions` | 版本号来源、`sync-versions.sh` 一致性校验与发布门禁 |

> 新增 skill 时**必须**同步本表——漏索引时系统仍会挂载它，但 AI 很难主动想到加载，
> 等于沉淀白写。
