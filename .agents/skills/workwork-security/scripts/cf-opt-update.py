#!/usr/bin/env python3
"""cf-opt-update: 定时探测 Cloudflare 优选边缘 IP，并更新 opt.yinghuoren.com 的 A 记录。

方法论见 ~/work/workwork/.agents/skills/workwork-security/references/edge-opt.md：
- 候选池：Cloudflare 官方 IPv4 段，每个 /24 随机采样 1 个地址；
- 阶段一：GET /cdn-cgi/trace 直连候选边缘（CF 边缘直接响应、不回源），测 TCP+TLS+TTFB；
- 阶段二：GET /speedtest/{0,size} 直连自有测速端点（回源到 nginx/admin），串行控频测 TTFB 与单连接吞吐；
- 阶段三：4 连接并行聚合复核（单连接排名跨窗口不可信）；
- 判定：聚合吞吐显著优于现役（默认 1.3x）且 TTFB 不劣化时才更新；现役不可达则必换；
  更新保持两条 A 记录、/16 分集，TTL 300、DNS only。
- CF API token 的 IP 白名单只放服务器出口，因此 API 调用必须经本机 xray http 代理（出口即服务器）。
- 测速/探测一律直连（--noproxy），不能走代理，否则测到的是服务器视角。

安全：token 只从凭据文件读取，绝不写入日志或输出。
"""

import argparse
import ipaddress
import json
import random
import re
import socket
import ssl
import statistics
import sys
import time
from concurrent.futures import ThreadPoolExecutor, as_completed
from datetime import datetime, timedelta, timezone
from pathlib import Path
from urllib import error as urlerror
from urllib import request as urlrequest

HOME = Path.home()
STATE_DIR = HOME / ".agents/.state/cf-opt"
CACHE_FILE = STATE_DIR / "cidrs-v4.txt"
STATE_FILE = STATE_DIR / "state.json"
LOG_DIR = STATE_DIR / "runs"

TOKEN_FILE = HOME / "work/dogod/cf_token_and_cert/cf_account_token"
ZONE_NAME = "yinghuoren.com"
RECORD_NAME = "opt.yinghuoren.com"
API_PROXY = "http://127.0.0.1:10809"
SNI = "yinghuoren.com"
TZ = timezone(timedelta(hours=8))

TRACE_PATH = "/cdn-cgi/trace"
SPEEDTEST_PATH = "/speedtest/{}"
SSL_CTX = ssl.create_default_context()


# ---------------------------------------------------------------- 基础工具

def now_local():
    return datetime.now(TZ)


def ensure_dirs():
    STATE_DIR.mkdir(parents=True, exist_ok=True)
    LOG_DIR.mkdir(parents=True, exist_ok=True)


def log_line(obj):
    ensure_dirs()
    date = now_local().strftime("%Y-%m-%d")
    path = LOG_DIR / (date + ".log")
    payload = {"ts": now_local().isoformat(timespec="seconds")}
    payload.update(obj)
    with path.open("a", encoding="utf-8") as fh:
        fh.write(json.dumps(payload, ensure_ascii=False) + "\n")


def load_state():
    try:
        return json.loads(STATE_FILE.read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return {}


def save_state(state):
    ensure_dirs()
    STATE_FILE.write_text(json.dumps(state, ensure_ascii=False, indent=2), encoding="utf-8")


def net16(ip):
    return int(ipaddress.IPv4Address(ip)) >> 16


def median(values, default=0.0):
    return statistics.median(values) if values else default


# ---------------------------------------------------------------- 直连探测

def fetch_direct(ip, path, timeout, read_bytes=0):
    """直连候选边缘 IP（SNI 固定为业务域名），返回耗时与响应信息。"""
    info = {"ip": ip, "connect": 0.0, "tls": 0.0, "ttfb": 0.0, "total": 0.0,
            "status": 0, "bytes": 0, "error": None}
    sock = None
    t_start = time.perf_counter()
    try:
        sock = socket.create_connection((ip, 443), timeout=timeout)
        info["connect"] = time.perf_counter() - t_start
        sock.settimeout(timeout)
        t0 = time.perf_counter()
        ssock = SSL_CTX.wrap_socket(sock, server_hostname=SNI)
        info["tls"] = time.perf_counter() - t0
        t0 = time.perf_counter()
        req = ("GET %s HTTP/1.1\r\nHost: %s\r\nUser-Agent: cf-opt-update/1.0\r\n"
               "Accept: */*\r\nConnection: close\r\n\r\n") % (path, SNI)
        ssock.sendall(req.encode("ascii"))
        buf = b""
        while b"\r\n\r\n" not in buf:
            chunk = ssock.recv(8192)
            if not chunk:
                break
            buf += chunk
            if len(buf) > 128 * 1024:
                break
        info["ttfb"] = time.perf_counter() - t0
        head = buf.split(b"\r\n\r\n", 1)[0].decode("latin1", "replace")
        match = re.match(r"HTTP/\d(?:\.\d)?\s+(\d{3})", head)
        info["status"] = int(match.group(1)) if match else 0
        if read_bytes > 0:
            body = buf.split(b"\r\n\r\n", 1)[1] if b"\r\n\r\n" in buf else b""
            got = min(len(body), read_bytes)
            while got < read_bytes:
                chunk = ssock.recv(min(65536, read_bytes - got))
                if not chunk:
                    break
                got += len(chunk)
            info["total"] = time.perf_counter() - t0
            info["bytes"] = got
            info["download"] = max(0.001, info["total"] - info["ttfb"])
        ssock.close()
    except Exception as exc:  # 网络错误是测速常态，逐 IP 记录，不抛
        info["error"] = "%s: %s" % (type(exc).__name__, exc)
    finally:
        if sock is not None:
            try:
                sock.close()
            except OSError:
                pass
    return info


def probe_trace(ips, timeout, concurrency):
    """阶段一：/cdn-cgi/trace 粗筛（不回源），返回成功的样本。"""
    results = []
    with ThreadPoolExecutor(max_workers=concurrency) as pool:
        futures = {pool.submit(fetch_direct, ip, TRACE_PATH, timeout): ip for ip in ips}
        for fut in as_completed(futures):
            item = fut.result()
            if item["status"] == 200 and not item["error"]:
                results.append(item)
    return results


def probe_speedtest(ip, timeout, size_bytes):
    """阶段二：串行测 3 次 TTFB + 1 次吞吐（调用方负责限频）。"""
    ttfb = []
    for _ in range(2):
        item = fetch_direct(ip, SPEEDTEST_PATH.format(0), timeout)
        if item["status"] == 200:
            ttfb.append(item["ttfb"])
        time.sleep(0.3)
    speed = 0.0
    item = fetch_direct(ip, SPEEDTEST_PATH.format(size_bytes), timeout, read_bytes=size_bytes)
    if item["status"] == 200 and item["bytes"] > 0 and item.get("download"):
        speed = item["bytes"] / item["download"]
    return {"ip": ip, "ttfb": median(ttfb), "speed": speed,
            "ok": bool(ttfb) and speed > 0, "samples": len(ttfb)}


def aggregate(ip, timeout, chunk, conns, rounds):
    """阶段三：多连接并行下载，取每轮聚合吞吐。"""
    per_round = []
    for _ in range(rounds):
        with ThreadPoolExecutor(max_workers=conns) as pool:
            futures = [pool.submit(fetch_direct, ip, SPEEDTEST_PATH.format(chunk), timeout,
                                   read_bytes=chunk)
                       for _ in range(conns)]
            results = [f.result() for f in futures]
        total_bytes = sum(r["bytes"] for r in results)
        elapsed_body = max((r.get("download") or 0.0) for r in results) if results else 0.0
        if elapsed_body > 0 and total_bytes > 0:
            per_round.append(total_bytes / elapsed_body)
        time.sleep(1.0)
    return {"ip": ip, "speed": median(per_round), "rounds": per_round}


# ---------------------------------------------------------------- 候选池

def fetch_cidrs(force=False):
    """CF 官方 IPv4 段；缓存 7 天。经代理获取（cloudflare.com 直连不稳）。"""
    if not force and CACHE_FILE.exists():
        age = time.time() - CACHE_FILE.stat().st_mtime
        if age < 7 * 86400:
            text = CACHE_FILE.read_text(encoding="utf-8")
            cidrs = [line.strip() for line in text.splitlines() if line.strip()]
            if cidrs:
                return cidrs
    opener = urlrequest.build_opener(
        urlrequest.ProxyHandler({"http": API_PROXY, "https": API_PROXY}))
    req = urlrequest.Request("https://api.cloudflare.com/client/v4/ips",
                             headers={"User-Agent": "cf-opt-update/1.0"})
    try:
        with opener.open(req, timeout=20) as resp:
            text = resp.read().decode("utf-8", "replace")
    except Exception:
        with urlrequest.urlopen(req, timeout=20) as resp:
            text = resp.read().decode("utf-8", "replace")
    cidrs = json.loads(text)["result"]["ipv4_cidrs"]
    if not cidrs:
        raise RuntimeError("Cloudflare IP 段列表为空")
    ensure_dirs()
    CACHE_FILE.write_text("\n".join(cidrs) + "\n", encoding="utf-8")
    return cidrs


def build_pool(cidrs, size, current_ips):
    """每个 /24 随机取 1 个地址。"""
    bases = []
    for cidr in cidrs:
        try:
            net = ipaddress.ip_network(cidr, strict=False)
        except ValueError:
            continue
        if net.version != 4:
            continue
        if net.prefixlen >= 24:
            bases.append(int(net.network_address))
            continue
        for sub in net.subnets(new_prefix=24):
            bases.append(int(sub.network_address))
    bases = sorted(set(bases))
    if not bases:
        raise RuntimeError("没有可用的 /24 候选")
    count = min(size, len(bases))
    picked = random.sample(bases, count)
    pool = [str(ipaddress.IPv4Address(base + random.randint(1, 254))) for base in picked]
    for ip in current_ips:
        if ip not in pool:
            pool.append(ip)
    return pool


# ---------------------------------------------------------------- Cloudflare API

def read_token():
    if not TOKEN_FILE.exists():
        raise SystemExit("缺少 CF token 文件: %s" % TOKEN_FILE)
    token = TOKEN_FILE.read_text(encoding="utf-8").strip()
    if not token:
        raise SystemExit("CF token 文件为空")
    return token


def cf_api(method, path, body=None):
    token = read_token()
    url = "https://api.cloudflare.com/client/v4" + path
    data = json.dumps(body).encode("utf-8") if body is not None else None
    req = urlrequest.Request(url, data=data, method=method, headers={
        "Authorization": "Bearer " + token,
        "Content-Type": "application/json",
    })
    opener = urlrequest.build_opener(
        urlrequest.ProxyHandler({"http": API_PROXY, "https": API_PROXY}))
    try:
        with opener.open(req, timeout=25) as resp:
            payload = json.loads(resp.read().decode("utf-8"))
    except urlerror.HTTPError as exc:
        detail = exc.read().decode("utf-8", "replace")[:400]
        raise RuntimeError("CF API %s %s -> HTTP %s: %s" % (method, path, exc.code, detail))
    except urlerror.URLError as exc:
        raise RuntimeError("CF API 不可达（检查本机代理 %s 是否在运行）: %s" % (API_PROXY, exc.reason))
    if not payload.get("success"):
        raise RuntimeError("CF API 业务失败: %s" % json.dumps(payload.get("errors"), ensure_ascii=False))
    return payload["result"]


def get_zone_id():
    zones = cf_api("GET", "/zones?name=" + ZONE_NAME)
    if not zones:
        raise RuntimeError("未找到 zone " + ZONE_NAME)
    return zones[0]["id"]


def list_records(zone_id):
    records = cf_api("GET", "/zones/%s/dns_records?name=%s&type=A" % (zone_id, RECORD_NAME))
    return [r for r in records if r.get("content")]


def patch_record(zone_id, record_id, ip):
    return cf_api("PATCH", "/zones/%s/dns_records/%s" % (zone_id, record_id),
                  {"content": ip, "ttl": 300})


# ---------------------------------------------------------------- 决策

def choose_targets(records, current_ips, agg, stage2, state, args):
    """返回 (action, mapping: record_id -> new_ip, reason)。"""
    now_speed = {ip: agg.get(ip, {}).get("speed", 0.0) for ip in current_ips}
    best_now = max(now_speed.values()) if now_speed else 0.0
    worst_now = min(now_speed.values()) if now_speed else 0.0

    cands = [(ip, data["speed"]) for ip, data in agg.items() if ip not in current_ips]
    cands.sort(key=lambda item: item[1], reverse=True)
    if not cands:
        return "keep", {}, "没有完成聚合复核的候选"

    top_ip, top_speed = cands[0]
    top_ttfb = stage2.get(top_ip, {}).get("ttfb", 0.0)
    best_now_ttfb = min((stage2.get(ip, {}).get("ttfb", 999.0) for ip in current_ips), default=999.0)

    ratio = args.ratio
    last = state.get("last_change_ts")
    if last:
        try:
            elapsed = time.time() - float(last)
            if elapsed < 55 * 60:
                ratio = max(ratio, 1.6)  # 冷却期内提高门槛，避免每小时抖动
        except (TypeError, ValueError):
            pass

    if best_now <= 0:
        action, reason = "replace_all", "现役全部不可达"
    elif top_speed >= best_now * ratio:
        action = "replace_all" if top_speed >= best_now * 1.45 else "replace_worst"
        reason = "候选聚合 %.2f MB/s >= 现役最佳 %.2f MB/s x%.2f" % (
            top_speed / 1048576, best_now / 1048576, top_speed / best_now)
    elif worst_now < best_now * 0.6 and top_speed > worst_now * 1.2:
        action, reason = "replace_worst", "现役最差 %.2f MB/s 明显退化" % (worst_now / 1048576)
    else:
        return "keep", {}, "候选未达更新阈值（top %.2f vs 现役最佳 %.2f MB/s）" % (
            top_speed / 1048576, best_now / 1048576)

    if top_ttfb > best_now_ttfb * args.ttfb_slack and best_now_ttfb > 0:
        return "keep", {}, "候选 TTFB %.0fms 劣于现役最佳 %.0fms 过多" % (top_ttfb * 1000, best_now_ttfb * 1000)

    ordered = sorted(records, key=lambda r: now_speed.get(r["content"], 0.0))
    picks = []
    seen_16 = set()
    for ip, _speed in cands:
        if net16(ip) in seen_16:
            continue
        seen_16.add(net16(ip))
        picks.append(ip)
        if len(picks) >= 2:
            break

    mapping = {}
    if action == "replace_worst":
        worst_record = ordered[0]
        if picks:
            mapping[worst_record["id"]] = picks[0]
    else:  # replace_all
        for record, ip in zip(ordered, picks):
            if record["content"] != ip:
                mapping[record["id"]] = ip
    return action, mapping, reason


# ---------------------------------------------------------------- 主流程

def parse_args(argv):
    parser = argparse.ArgumentParser(description="探测并更新 opt.yinghuoren.com 的 CF 优选 IP")
    parser.add_argument("--dry-run", action="store_true", help="只测速与决策，不改 DNS")
    parser.add_argument("--force", action="store_true", help="忽略阈值，按聚合复核最优结果更新")
    parser.add_argument("--pool-size", type=int, default=600, help="候选 IP 数（默认 600）")
    parser.add_argument("--top", type=int, default=10, help="进入单连接测速的候选数（默认 10）")
    parser.add_argument("--aggregate-top", type=int, default=3, help="进入聚合复核的候选数（默认 3）")
    parser.add_argument("--trace-timeout", type=float, default=4.0, help="阶段一超时秒（默认 4）")
    parser.add_argument("--speed-timeout", type=float, default=10.0, help="下载测速超时秒（默认 10）")
    parser.add_argument("--speed-size", type=int, default=4 * 1024 * 1024, help="单连接下载字节（默认 4MiB）")
    parser.add_argument("--agg-chunk", type=int, default=2 * 1024 * 1024, help="聚合单连接字节（默认 2MiB）")
    parser.add_argument("--agg-conns", type=int, default=4, help="聚合连接数（默认 4）")
    parser.add_argument("--agg-rounds", type=int, default=2, help="聚合轮数（默认 2）")
    parser.add_argument("--ratio", type=float, default=1.35, help="更新阈值：候选/现役最佳（默认 1.35）")
    parser.add_argument("--ttfb-slack", type=float, default=2.5, help="TTFB 最大劣化倍数（默认 2.5）")
    parser.add_argument("--verbose", action="store_true")
    return parser.parse_args(argv)


def main(argv=None):
    args = parse_args(argv or sys.argv[1:])
    random.seed()
    started = time.time()
    ensure_dirs()

    records = list_records(get_zone_id())
    current_ips = [r["content"] for r in records]
    if not current_ips:
        raise SystemExit("%s 没有 A 记录，拒绝继续" % RECORD_NAME)
    print("现役记录: %s" % ", ".join(current_ips))

    cidrs = fetch_cidrs()
    pool = build_pool(cidrs, args.pool_size, current_ips)
    print("候选池: %d 个 IP（来自 %d 个 CIDR 段，每个 /24 采样 1 个）" % (len(pool), len(cidrs)))

    t1 = time.time()
    stage1 = probe_trace(pool, args.trace_timeout, 24)
    stage1.sort(key=lambda r: r["ttfb"])
    print("阶段一: %d/%d 可达，耗时 %.1fs" % (len(stage1), len(pool), time.time() - t1))
    top = stage1[:args.top]
    if not top:
        raise SystemExit("阶段一无任何可用候选（检查网络或 SNI）")

    t2 = time.time()
    stage2 = {}
    for idx, item in enumerate(top, 1):
        ip = item["ip"]
        stage2[ip] = probe_speedtest(ip, args.speed_timeout, args.speed_size)
        if args.verbose:
            print("  [%d/%d] %s ttfb=%.0fms speed=%.2f MB/s" % (
                idx, len(top), ip, stage2[ip]["ttfb"] * 1000, stage2[ip]["speed"] / 1048576))
    for ip in current_ips:
        stage2[ip] = probe_speedtest(ip, args.speed_timeout, args.speed_size)
        time.sleep(0.35)
    print("阶段二: 单连接测速完成，耗时 %.1fs" % (time.time() - t2))

    cand_sorted = sorted(
        [ip for ip in stage2 if ip not in current_ips and stage2[ip]["ok"]],
        key=lambda ip: stage2[ip]["speed"], reverse=True)
    failed = [ip for ip in stage2 if ip not in current_ips and not stage2[ip]["ok"]]
    if failed:
        print("阶段二失败（不进入聚合复核）: %s" % ", ".join(failed))
    agg_targets = cand_sorted[:args.aggregate_top]
    if not agg_targets:
        raise SystemExit("阶段二没有可用候选")
    t3 = time.time()
    agg = {}
    for ip in agg_targets + current_ips:
        agg[ip] = aggregate(ip, args.speed_timeout, args.agg_chunk, args.agg_conns, args.agg_rounds)
    print("阶段三: 聚合复核完成，耗时 %.1fs" % (time.time() - t3))

    state = load_state()
    if args.force:
        action, mapping, reason = "force", {}, "强制按最优结果更新"
        ordered = sorted(records, key=lambda r: agg.get(r["content"], {}).get("speed", 0.0))
        picks, seen16 = [], set()
        for ip in cand_sorted:
            if net16(ip) in seen16:
                continue
            seen16.add(net16(ip))
            picks.append(ip)
        if picks:
            mapping[ordered[0]["id"]] = picks[0]
        if len(picks) > 1 and len(ordered) > 1:
            mapping[ordered[1]["id"]] = picks[1]
    else:
        action, mapping, reason = choose_targets(records, current_ips, agg, stage2, state, args)

    applied = []
    if mapping and not args.dry_run:
        zone_id = get_zone_id()
        for record_id, ip in mapping.items():
            patch_record(zone_id, record_id, ip)
            applied.append({"record": record_id, "ip": ip})
        if applied:
            state["last_change_ts"] = time.time()
            new_records = list_records(zone_id)
            state["last_ips"] = [r["content"] for r in new_records]
            save_state(state)
            print("更新后记录: %s" % ", ".join(r["content"] for r in new_records))

    summary = {
        "action": action,
        "reason": reason,
        "applied": applied,
        "dry_run": args.dry_run,
        "current": current_ips,
        "candidates": [{"ip": ip, "agg_mbps": round(agg[ip]["speed"] / 1048576, 2),
                        "single_mbps": round(stage2[ip]["speed"] / 1048576, 2),
                        "ttfb_ms": round(stage2[ip]["ttfb"] * 1000)}
                       for ip in cand_sorted if ip in agg],
        "current_perf": [{"ip": ip, "agg_mbps": round(agg[ip]["speed"] / 1048576, 2),
                          "single_mbps": round(stage2[ip]["speed"] / 1048576, 2),
                          "ttfb_ms": round(stage2[ip]["ttfb"] * 1000)}
                         for ip in current_ips],
        "elapsed_s": round(time.time() - started, 1),
    }
    log_line({"kind": "run", **summary})
    print(json.dumps(summary, ensure_ascii=False, indent=2))
    if args.dry_run:
        print("dry-run：未修改 DNS")
    elif applied:
        print("已更新: %s" % ", ".join("%s -> %s" % (a["record"], a["ip"]) for a in applied))
    else:
        print("保持现役记录不变")
    return 0


if __name__ == "__main__":
    sys.exit(main())
