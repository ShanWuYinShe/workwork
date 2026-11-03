# XHTTP 数据面：分片上限与 413 静默停滞（细节）

> 本文是 skill `workwork-security` 的细节补充；契约表与线上排查命令在 SKILL.md，本文补充字段来源、完整机制与本地复现。

## 字段来源

字段名与回退值以 Xray 26.6.27 源码为准：`transport/internet/splithttp/config.go` 的 `GetNormalizedScMaxEachPostBytes` / `GetNormalizedScMaxBufferedPosts`；JSON 侧是 `infra/conf/transport_internet.go` 的 `SplitHTTPConfig`（`Int32Range`，可写数字）。

## 故障机制（完整链路）

- 上行 POST body 超过服务端 `scMaxEachPostBytes` 时，服务端回 **413 且不读请求体**（`splithttp/hub.go`）。
- 客户端 `PostPacket` 见非 200 → `uploadPipeReader.Interrupt()`，上行静默停摆。
- 应用侧看到的是**响应体停滞**（请求体写满后 0 字节响应直到超时）。
- 服务端那句 `Too large upload. scMaxEachPostBytes is set to ...` 是 **Info 级**，生产 `loglevel=warning` 下完全不可见。xhttp 自愈链路对 413 计数并告警（`internal/proxy/xhttp_health.go`，**只通知不重启**——413 是配置不匹配，重启不改变配置、只会让全体代理周期性抖动）。

## 本地复现（容器跑服务端 + 宿主机跑客户端；明文 TCP xhttp 即可，无需证书）

- 服务端 1MiB 上限 + 客户端 3MiB 分片 → 10MB 上传卡死 15s，目标 0 字节，服务端 2 次 413；
- 服务端 4MiB 上限 + 客户端 3MiB 分片 → 10MB 上传 0.28s，200，目标收全 10485760 B。

**陷阱**：目标服务在宿主机时，Xray 26.6.27 的 freedom 出站**默认黑洞私网地址**（`proxy/freedom/freedom.go` 的 `defaultBlockPrivateRule`：0.0.0.0/8、10/8、127/8、172.16/12、192.168/16、fc00::/7 等）。测试配置需临时加 `"settings": {"finalRules": [{"action": "allow"}]}` 才能打到宿主机，否则日志出现 `proxy/freedom: blocked target: ... blackholing connection`，会把「目标不可达」误判成传输故障。

复现材料放在 `.workwork/xhttp-post-limit-verify/`（服务端/客户端配置与各组日志；中间产物，可能已被清理，需要时按本节步骤重建）。
