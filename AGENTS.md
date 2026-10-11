# workwork

Docker 代理基础设施：Xray VLESS XHTTP 代理 / Nginx 反代 / Go 管理面板。

通用约束（Git 规范、Commit 格式、验证流程、工具链偏好、Docker 等）以全局 `~/AGENTS.md` 为准（各 AI 工具的全局指令文件均链接到该文件），本文件只做项目特有补充，不得放宽或违背全局约束。

AI Agent 工作目录：所有 AI agent 工作期间产生的中间文件写入项目根 `.workwork/`（已 `.gitignore`）。注意与运行时数据目录 `.wework/`（服务配置、日志挂载源，同样 gitignored；曲库已迁至 music_player/music_server 的 `deploy/data/library/`）区分：两者名字相近但用途无关，不要混淆。

## 硬约束

所有服务功能必须隔离在 Docker 容器内。 禁止修改宿主机环境设置（iptables、sysctl、网络配置），创建 Docker 网络除外。IPv4 路由走 Docker bridge NAT；IPv6 路由走 ipvlan L3。admin 服务经自建 docker API 白名单代理（compose `docker-proxy` 服务，method+path 级白名单见 `services/docker_proxy/main.go`）和 `containerExec()` 操作其他容器——新增 docker API 调用时必须同步白名单与集成测试。

## 发布门禁（硬性）：先本地验证，再发布

禁止把未经本地验证的改动推向线上：在 `git push origin main` 与服务器 `make deploy-changed` 之前，必须先完成本节本地验证。

1. `make check` —— 本地门禁：gofmt / go vet / go test / 前端构建（admin 改动的最低验证项）。
2. `make build <受影响服务...>` —— 受影响服务的镜像必须在本地构建通过（等价 `docker compose build`；经 Makefile 自动烙入本次 `git describe` 版本，不是 "dev"）。
3. 冒烟：本地能独立启动的服务，`make up <svc...>` 后等 healthy，并按 README「版本排查」核对健康端点版本指纹。
4. 以下服务本地不具备启动条件（本地仓库默认没有 `.env` / `cert/` / `.wework/`，也没有外部 `ipvlan_docker` 网络），做到第 2 步即视为本地验证通过，上线可靠性由服务器 `deploy-changed.sh` 的部署后自校验（等 healthy + 版本指纹核对，不通过即 fail）兜底：
   - `nginx`（需 `cert/` 证书与 admin 渲染到 `.wework/generated/` 的运行时配置）；
   - `xray`（需外部 `ipvlan_docker` IPv6 网络）。
   不要为了验证而在本地伪造线上证书或密钥。

例外：纯文档/注释改动（`*.md`、代码注释）可跳过第 2、3 步，但仍执行第 1 步中有意义的部分。

## 架构速览

完整设计叙事（服务分工、网络拓扑、IPv6 寻址、RBAC 矩阵）见 [`docs/architecture.md`](./docs/architecture.md)，需要时再读，不要常驻记忆。本节只保留做错会出事的禁令：

- 不要让 xray 加入 `internal_docker`（原因见 docs）
- XHTTP 模式必须是 `packet-up`，不要改成 `stream-up`
- 不要为 IPv4 入站加其他转发方式（宿主 iptables DNAT 等）
- 改 `services/admin/configs/` 下模板 = rebuild admin（模板与二进制原子对应）；模板渲染出的运行时配置只在 xray/nginx 启动时读取，`deploy-changed.sh` 会连带 `restart` 对应服务——改该脚本时勿丢这条映射，否则新配置渲染了却静默不生效

## 关键约定

- 文档单一来源：架构"是什么"只写在 `docs/architecture.md`，AGENTS 不复述实现细节。文档与可验证实现冲突时以代码为准并修正文档，不要按错误描述继续。
- 配置字段核对: Xray、Nginx、Docker 等配置以官方源码/文档为准；优先读取官方 GitHub 仓库的 Go struct JSON tag 或官方文档确认字段名大小写、合法值范围和字段语义。
- Never use `RemoteAddr` for auth — 所有流量经 Docker bridge，看起来都是内网地址。
- Internal auth: `INTERNAL_TOKEN`（启动时自动生成，存储在 `.wework/.internal-token`）；fail2ban 使用独立 scoped token（`.wework/fail2ban/token`）。`INTERNAL_TOKEN` 默认全权（仓库代码不主动发起携带该 token 的调用，它服务于外部自动化，默认收紧会静默破坏它们）；配置 `INTERNAL_TOKEN_ALLOWED_PREFIXES`（逗号分隔路径前缀）后只豁免白名单内路径，白名单外的使用会以 `[security]` 留痕——新部署建议按日志逐步收敛。携带方式：`X-Internal-Token` 头或 `Authorization: Bearer <token>`（nginx 透传，外部自动化经公网可用）。持合法 token 的请求豁免全部防护层（nginx 限速与 IP 封禁按"携带即豁免"，admin 层恒时比较验真伪后豁免应用层限流与 IP 封禁）——授权请求不被防护误伤；伪造 token 在 admin 校验失败后照常被限流封禁，防线不弱化。
- Secrets: gitignored `configs/`、`cert/`、`.wework/`。本地 `.env` 覆盖默认值。
- Xray 客户端归属: 非 admin 用户只能管理自己的 Xray 客户端（每人一个）。Admin 可看全部。
- 配置文件写入：统一使用 `atomicWriteFile`（写 .tmp → rename），防止写到一半崩溃导致配置损坏。禁止直接 `os.WriteFile` 写配置文件。
- 错误响应：业务错误用 `writeError(w, msg, code)`（msg 返回客户端）；内部错误（文件 I/O、Docker API、第三方 API 等可能泄露内部状态的）必须用 `writeInternalError(w, err)`（只返回固定 "Internal error"，真实错误记日志）。
- 新增 API 端点：在 `main.go` 的 `mux.HandleFunc(...)` 区域注册，所有认证端点用 `auth.RequireRole("viewer")` 或 `auth.RequireRole("admin")` 包裹；健康检查用无认证的裸 handler。handler 实现放对应 internal/ 域包（platform←auth←accounts/proxy←infra/downloads/transfer/nginxcfg，依赖单向无环）；大上传端点必须同步在 main.go 的 init() 里 `platform.ExemptBodyLimit(...)` 声明并在 main_test.go 契约测试登记。
- 审计留痕：变更类端点（增删改、启停、发布、封禁等）必须在成功路径记 `log.Printf("[security] <动作> <关键标识> by=%s", actor)`，actor 取 `auth.GetUserFromContext(r)`；只读端点不需要。用户可控的标识（文件名、昵称等）必须 `%q` 转义——换行注入会伪造出假审计行。请求日志（RequestLogMiddleware）已带 user 字段兜底。
- 环境变量：通过 `platform.GetEnv()` 读取（见 `services/admin/internal/platform/env.go`），不直接用 `os.Getenv`。

