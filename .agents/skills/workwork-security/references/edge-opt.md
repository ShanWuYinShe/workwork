# Cloudflare 优选入口：测速方法与排障细节

> 本文是 skill `workwork-security` 的细节补充。SKILL.md 只留现役结论；换优选 IP、测速或排查代理链路时读本文。

## 方案与客户端拆分

- `opt.yinghuoren.com` = DNS only 的 A 记录，直接指向实测最优的 CF anycast 边缘 IP；源站回源仍走 `yinghuoren.com` 的 mTLS。
- 链路瓶颈在「客户端→CF 边缘」段：免费版对武汉电信调度差（RTT 240-280ms，晚高峰 TLS 0.5-6s 且常超时）；服务器与源站侧健康。
- 客户端配置只改 `address` 为 opt 域名，`serverName`/`Host`/SNI 仍是 `yinghuoren.com`（SNI 用 opt 域名会证书不匹配）。
- 面板生成配置已内建拆分：服务端 `.env` 设 `XHTTP_ADDRESS=opt.yinghuoren.com`（`XHTTP_DOMAIN` 保持 yinghuoren.com 作 SNI），改后重启 admin 容器生效；手工改本机 `config.jsonc` 仅作临时验证。

## 现役 IP 与换 IP 纪律

- 现役：**162.159.154.177 + 162.159.158.122**（TTL 300、DNS only、分属两个 /16 保持路由分集）。
- 换 IP 用 CF API PATCH（TTL 300）；本机 xray 有 DNS 缓存，数分钟内仍走旧 IP 属正常，TTL 过期自愈。
- CF 边缘 anycast 质量会漂移：优选 IP 变差时重新探测（`curl --noproxy '*' --resolve yinghuoren.com:443:IP https://yinghuoren.com/nginx-health` 测 connect/tls/total，超时 4s）并更新 DNS 记录。

## 测速方法（唯一可信路径）

**只能用自己的 `/speedtest/{size}` 端点直连测**：它测的是「客户端→CF 候选边缘→CF 回源→nginx→admin」完整链路，与客户端实际访问路径一致；TTFB(size=0) 与吞吐(size=8MiB) 都要看。

```bash
curl --noproxy '*' --resolve yinghuoren.com:443:<候选IP> \
  https://yinghuoren.com/speedtest/{0|8388608}
```

**cfst 标准流程**（XIU2/CloudflareSpeedTest v2.3.5，darwin arm64 二进制在 `.workwork/cf-opt/cfst/`——该目录属中间产物、可能已被清理，需要时经本机 xray 代理从 GitHub release 重新下载）：2.5 分钟完成全段粗筛 + top10 下载测速。

```bash
./cfst -url "https://yinghuoren.com/speedtest/33554432" -dn 10 -t 4 -p 15 -o result.csv
```

要点：① 自有测速端点须给**大文件**（32MB）——8MB 会让 TCP 窗口没爬满就结束，速度低估一个量级；② 下载阶段串行（10 IP×10s）天然低于 20req/10s 限流阈值，无需 token；延迟阶段是纯 TCPing 零负载；③ 默认每 /24 随机 1 个 IP 的采样优于手取 .1（黑马 IP 全是随机位发现的）。

**必须同窗口双法交叉复核**：cfst 单连接排名 ≠ 聚合表现，跨窗口必翻车（cfst top 的候选在 4 连接聚合复测中可能归零或翻车，反之亦然）。聚合复核 = 4×curl 并行 4MB 求和。

手搓并发测速必须 sleep 控频：4 并发 × 6 IP × 2 轮（~48 请求、无间隔）即触发 Go 层 20req/10s 自动 ban 30min + ipBlocker 403。

## 测量陷阱

1. **端点选错会让排名完全反过来**：用 `speed.cloudflare.com/__down` 测出的候选，改用自有 /speedtest 后吞吐与 TTFB 大幅恶化——CF 公开端点的数据在 CF 网内产生，不经过「回源→nginx→admin」。
2. **不要用 xray 隧道 + 自有端点**：服务端 freedom 出站再访问 `yinghuoren.com` 会绕第二圈 CF（实测 30s 只下 286KB）。优选测量一律用上面的 curl `--resolve` 直连。
3. 本机 shell 带 `http_proxy/https_proxy=127.0.0.1:10809`，测速/探测必须 `--noproxy '*'`，否则测到的是「服务器→CF」视角。
4. 经 CF 访问 `/nginx-health` 返 404 属正常（443 上无该 location，落 `location /`）；404 恰证明回源成功，真失败是连接层 `000`。
5. 「吞吐最差」优先于「吞吐中位」判断；同时间窗多采样，单次结果不可信（同一 IP 分钟级可在 2.1↔5.7 MB/s 波动）。
6. 携带合法 `Authorization: Bearer <INTERNAL_TOKEN>` 可豁免全部防护层；伪造 token 无效。

## 扫描规模：必须分两阶段

**一次性全量扫描会把本机 IP 打进自家防护**：超出 Go 层 `20 req/10s` 阈值即触发 ipBlocker 403 + admin 内存限流 30min ban，全部样本报废。扫描必须分两阶段：

1. 阶段一粗筛用 `/cdn-cgi/trace`（CF 边缘直答**不回源**，随便扫）：
   ```bash
   curl --noproxy '*' --resolve yinghuoren.com:443:<IP> \
     -w '%{time_connect} %{time_appconnect} %{time_starttransfer} %{http_code}' \
     https://yinghuoren.com/cdn-cgi/trace
   ```
   836 候选 24 并发 3 分钟跑完。
2. 阶段二终测才用自有 /speedtest：对 top 24 **串行 + 每请求 sleep 0.5s**（任何 10s 窗口 ≤6 请求，远低于 20req/10s），每 IP 3×TTFB(size=0) + 3×吞吐(8MiB)。

## 403 / 429 分层排障（本机 IP 被自家防护拦截时）

- **403** = ipBlocker 持久封禁（nginx 层 `blocked-ips.conf` + admin 层）：持 INTERNAL_TOKEN 调 `POST /api/admin/security/unblock` `{"ip":"..."}`。
- **429** 有两种：nginx `limit_req`（无状态，停手即恢复）与 admin `authRateLimiter` 内存 ban（30min、不续期、到期自动重置）。后者用 `GET|POST /api/admin/security/rate-limit[/unblock]` 查询/解封。
- **两个解封 API 都要调**（`security/unblock` 与 `security/rate-limit/unblock`）。
- 调用路径：ssh 服务器后，token 由宿主 shell 读文件后传入容器（nginx 容器没有挂载 `.wework/.internal-token`，在容器内 `cat` 会失败）：
  ```bash
  TOKEN="$(cat .wework/.internal-token)"
  docker compose exec -T nginx curl -sS -H "X-Internal-Token: $TOKEN" \
    http://admin:8318/api/admin/security/rate-limit
  ```
  （`ADMIN_PORT` 见服务器 `.env`，当前 8318。）
- 坑：被封后 **TTFB 探针仍「正常」返回**（403 页也有 TTFB），只看 TTFB 会误判链路正常——「聚合下载全零 + TTFB 正常」= 先查是否被自家封禁（源站 `blocked-ips.conf` grep 自己 IP，或 403 正文含 nginx 字样）。

## CF API token 与 DoH

- CF API token（归档位置见用户级 `local-environment` 的 Cloudflare 管理 token 条目）的 IP 白名单**只放服务器出口 IP**（有意设计）：本机直连调用被拒（错误码 9109，v4/v6 相同）。正确且唯一用法是本机持 token + 经本机 xray http 入站代理（`http_proxy=http://127.0.0.1:10809`），出口即服务器。**不要把家宽 IP 加白（那是拆掉这道防线），也不要把 token 复制到服务器（凭据红线）。**
- DoH 候选可达性实测必须用 **RFC8484 wire format**：`?dns=<base64url 查询>` + `accept: application/dns-message`。JSON API（`?name=`）对 Google/Quad9 会 400，造成「不可用」误判——wire format 下 1.1.1.1/1.0.0.1/8.8.8.8/9.9.9.9 与 v6 解析器全部 200，且 8.8.8.8 证书含 IP SAN 可作 DOHL 目标。
  ```bash
  python3 -c "import base64,struct;h=struct.pack('>HHHHHH',0x1234,0x0100,1,0,0,0);n=b''.join(bytes([len(p)])+p.encode() for p in 'example.com'.split('.'))+b'\\x00';print(base64.urlsafe_b64encode(h+n+struct.pack('>HH',1,1)).decode().rstrip('='))"
  ```
- **example.com 权威 NS 已在 Cloudflare**（elliott/hera.ns.cloudflare.com）——它返回 CF IP 是真实答案，拿它检测「DNS 劫持」会误判；验证劫持应先查权威 NS，再用多个独立加密解析器交叉。

## 出站策略陷阱：prioritizeIPv6

- freedom `prioritizeIPv6: true` 会拖死无 AAAA 的目标：`github.com` 无 IPv6，开启后经代理访问 20s 超时（服务器 v4 直连 0.1s 正常），youtube/cloudflare 等有 AAAA 的正常——表现为「特定网站经代理打不开、其他正常」，极易误判为链路问题。
- `xray.json.tmpl` 必须保持 `prioritizeIPv6=false`。排障手法：源站 `curl -4/-6` 分栈对比 + 经隧道与直连源站同目标对比，即可区分「链路问题 / 出站策略问题」。
- 注意 `xray.json.tmpl` 渲染后必须是合法 JSON——JSON 结构内不能加 `//` 注释（`nginx.conf.tmpl` 才行），`TestXrayTemplateRendersValidJSON` 会拦。

## h3 / QUIC

- `XHTTP_ALPN=h3` 的客户端在晚高峰 QUIC 出站整段挂死（curl 000、15s 超时，direct 规则正常），同刻 h2/TCP 一切正常、服务器数据面 631×200/h——武汉电信晚高峰对 UDP 443 的 QoS。
- 因此面板默认 `XHTTP_ALPN=h2`（安全默认，TCP 专属 sockopt 保留）；h3 留作旋钮，仅适合 UDP 质量好的时段/网络，**晚高峰代理挂掉先查这里**（服务器 `.env` 一行 + 重启 admin）。
- 客户端 `config.jsonc` 若 h3-only，晚高峰同样会挂，`alpn` 改回 `["h2"]` 即恢复。本机 curl 8.7 无 http3，直连只能测 h2；h3 差异未直测。

## 方法要点

- 默认解析 IP 有 2.5-8s 坏样本；v6 各段全面劣于 v4，故暂未配 AAAA（以后要 v6 须先同样手法实测再加）。
- 单 IP 测速会骗人：某候选单连接最快，4 连接聚合复测排名翻转（152.1 聚合 8.7 / 147.1 8.4 / 31.35 仅 5.8 MB/s）——curl 单连接吞吐会被每连接瓶颈骗过；XHTTP packet-up 是多分片并发，**终测必须加多连接聚合**。
- **不要沿用的 IP**：`162.159.128.1`（TTFB 最差 2531ms）、`104.26.1.1`（吞吐最差 2.77 MB/s）、`104.26.6.1`（退化 3.62MB/s / 638ms）、`162.159.137.187`（三轮聚合 7.1/9.6/7.9，全场最弱退场）、`162.159.152.1`（同法仅 5.6 MB/s）。它们都只反映各自测量窗口，换 IP 必须重新按上面的方法实测。
