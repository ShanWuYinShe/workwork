# 多层防御实现细节

> 本文是 skill `workwork-security` 的细节补充。SKILL.md 只保留各层红线与判据；改动对应层时读本文。

## fail2ban 计数语义与误封教训

- 过滤规则只计**带错误凭证的 401**：failregex 按 `$remote_user` 非空区分（`^<HOST> - (?!- )\S+ \[`）。浏览器冷启动质询的无凭证 401 属正常登录流程，不计入。
- 因此 `maxretry=5` = 真实错密码 5 次；登出凭证覆盖的请求每次仍计 1 条，故不能把阈值取 3。
- 反模式：全量计数 + `maxretry=10`（曾两次误封合法用户：5min 内双冷启动 ≈12>10 次即封）。现行按凭证分流已修。
- 验证手法：构造 auth_log 样本行，在 nginx 镜像内跑 `fail2ban-regex` 断言命中/不命中集合。

## nginx 限速与封禁的 internal token 豁免

- 模板在 **http 级** 用 map 组合：`$has_internal_tok` → 空限速键（跳过 `limit_req`）/ `$blocked_final`（跳过 `return 403`）。
- 豁免只做存在性判断；token 真伪由 admin 恒时比较，伪造者照常落入应用层限流与封禁，防线不弱化。
- `transfer_dl` / `transfer_conn` 刻意不豁免（公开下载本就受自身并发约束）。
- 改 nginx 模板后先做语法预验证：抽取模板 http 级片段 + 模拟 `blocked-ips.conf` 的 map，用与 `versions.env` 钉死版本一致的镜像跑：
  ```bash
  docker run --rm -v <conf>:/conf:ro nginx:1.30.4-alpine nginx -t -c /conf/nginx.conf
  ```
  **`map` 指令只允许出现在 http 上下文**（曾误放 server 块致 nginx emerg，部署自校验拦下）。

## 限流/封禁解封的两个维度

- Go rate limiter：20 req/10s → 429 → 自动封禁 30min；IPv6 按 /64 聚合计数，换后缀不重置预算，封禁按具体 IP（2026-10-16 修）。
- **解封 API 必须两个维度都清**：整点计数 + /64 前缀预算。只清前者时同前缀其他地址仍被拦截。
- `GET /api/transfer/*` 公开下载豁免 Go 层限流（nginx 已有 1r/s + 3 并发约束，多连接下载器勿再用应用层限流惩罚）。

## 工具下载完整性校验（`internal/downloads`）

- admin 的 claude/xray 安装包下载比对 GitHub release asset 的 `digest`（`sha256:<hex>`；releases API 每个 asset 都带，实测 60/60）。
- 校验点在 `downloadArchive` 内、`rename`+manifest 落盘**之前**——下载目录会被 `/api/admin/*/downloads` 分发给所有代理客户端，未校验的字节落库即出库；失败只留下可丢弃临时文件（`defer os.Remove`），接口记 `[security] checksum verify failed`（含期望/实际值）。
- 上游缺摘要或非 sha256 算法时**跳过**校验而不拒绝下载（上游字段变化不应打断下载能力）。
- xray 侧此前零校验；claude 侧另有 `SHASUMS256.txt` 比对，保留为第二层。
- 回归护栏：`internal/downloads/digest_test.go` 的 handler 用例——把 `assetSHA256(assetDigest)` 断成空串即失败。

## docker API 白名单代理（`services/docker_proxy`）

- `(method, path)` 正则逐条锚定首尾；匹配前先剥 query，且**禁止二次 URL 解码**（`%252F` 变体会让「检查的串」与「上游执行的串」不一致，是白名单绕过的经典形态）。
- 引用具体容器的路径（`/containers/{id}/…`、`/exec/{id}/…`）额外过 compose 项目归属校验（`com.docker.compose.project` ∈ `ALLOWED_COMPOSE_PROJECTS`；exec 先 inspect 取 `ContainerID`）。inspect 失败 / 容器不存在 / label 缺失一律**拒绝**（fail-closed）。
- **无容器句柄的读端点**（`/containers/json`、`/events`）要求调用方自带允许项目的 label 过滤：`filters` 含 `com.docker.compose.project=<ALLOWED_COMPOSE_PROJECTS>`（docker SDK 的 map 形态与手工/旧版的数组形态都认）；缺失、JSON 不可解析或项目不在名单一律 403（fail-closed；此前是「直接放行 + 接受跨项目可见元数据」的残余风险，2026-10-16 收紧）。
- `/info`、`/version`、`/_ping` 仍直接放行（宿主级信息、不含容器元数据）。
- admin 的 8 个 docker 读调用点（7×`ContainerList` + 1×`Events`）全部经 `platform.ComposeProjectFilters(...)` 注入该过滤，正常路径零行为变化。
- `ALLOWED_COMPOSE_PROJECTS` 由 compose 从 `COMPOSE_PROJECT_NAME` 接线；两者不一致会让本栈自己的容器被整体拒绝、面板操作静默失效。
- 本地端到端复跑（真实 daemon）：
  ```bash
  docker run -d --name wk-proxy-test -p 127.0.0.1:23750:2375 \
    -e ALLOWED_COMPOSE_PROJECTS=test -v /var/run/docker.sock:/var/run/docker.sock:ro \
    workwork-docker-proxy:<tag>
  TEST_DOCKER_HOST=tcp://127.0.0.1:23750 TEST_COMPOSE_PROJECT=test \
    go test -run TestDockerProxyWhitelist ./internal/platform/
  ```
  该测试同时锁白名单、容器归属校验、无句柄端点作用域三种边界（OrbStack 下 `/var/run/docker.sock` 挂载可用）。
