"""dev-env CLI argument parser and dispatcher."""

import argparse
import sys

from dev.commands import (
    cmd_start, cmd_stop, cmd_teardown, cmd_restart, cmd_reset,
    cmd_shell, cmd_status, cmd_logs, cmd_wait, cmd_exec,
    cmd_init,
    cmd_backup, cmd_restore, cmd_list_backups,
    cmd_snapshot,
    cmd_generate, cmd_list_instances, cmd_xray_start, cmd_xray_stop,
    cmd_start_test, cmd_stop_test, cmd_reset_test, cmd_test,
    cmd_doctor, cmd_prune, cmd_completion, cmd_port_usage, cmd_version,
    cmd_ip, cmd_config, cmd_dashboard,
)
from dev.core import IPVLAN_SERVICES


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="dev.py",
        description="dev-env - Development Environment Manager",
    )
    sub = parser.add_subparsers(dest="cmd", required=True)

    # ── Environment ──────────────────────────────────
    sub.add_parser("start", help="Start development environment")
    p_stop = sub.add_parser("stop", help="Stop environment")
    p_stop.add_argument("--all", "-a", action="store_true", help="stop all dev projects")
    sub.add_parser("teardown", help="Destroy environment (delete all data)")
    p_restart = sub.add_parser("restart", help="Restart service(s)")
    p_restart.add_argument("service", nargs="?", help="service name (default: all)")
    sub.add_parser("reset", help="Reset all service data")
    p_shell = sub.add_parser("shell", help="Enter dev container")
    p_shell.add_argument("shell", nargs="?", default="fish", help="shell type")
    sub.add_parser("status", help="Show environment status")
    p_logs = sub.add_parser("logs", help="View service logs")
    p_logs.add_argument("service", nargs="?", help="service name")
    p_logs.add_argument("tail", nargs="?", default="50", help="tail lines")
    p_wait = sub.add_parser("wait", help="Wait for healthy services")
    p_wait.add_argument("--timeout", "-t", default=60, type=int, help="timeout seconds")
    p_exec = sub.add_parser("exec", help="Run command in dev container")
    p_exec.add_argument("args", nargs=argparse.REMAINDER, help="command")

    # ── Init ─────────────────────────────────────────
    sub.add_parser("init", help="Initialize environment (.env, checks)")

    # ── Backup ───────────────────────────────────────
    sub.add_parser("backup", help="Backup data (PostgreSQL + Redis)")
    sub.add_parser("list-backups", help="List available backups")
    p_restore = sub.add_parser("restore", help="Restore from backup")
    p_restore.add_argument("target", nargs="?", help="backup name (default: latest)")

    # ── Snapshot ─────────────────────────────────────
    p_snap = sub.add_parser("snapshot", help="Manage PostgreSQL snapshots")
    sp = p_snap.add_subparsers(dest="sub")
    sp.add_parser("list", help="List snapshots")
    p_sc = sp.add_parser("create", help="Create a snapshot")
    p_sc.add_argument("name", nargs="?", help="snapshot name")
    p_sr = sp.add_parser("restore", help="Restore a snapshot")
    p_sr.add_argument("name", nargs="?", default="latest", help="name or 'latest'")
    p_sd = sp.add_parser("delete", help="Delete a snapshot")
    p_sd.add_argument("name", help="snapshot name")

    # ── Xray ─────────────────────────────────────────
    sub.add_parser("generate", help="Generate Xray configs")
    sub.add_parser("list-instances", help="List Xray instances")
    sub.add_parser("xray-start", help="Start Xray proxy services")
    sub.add_parser("xray-stop", help="Stop Xray proxy services")

    # ── Test ─────────────────────────────────────────
    sub.add_parser("start-test", help="Start test environment")
    sub.add_parser("stop-test", help="Stop test environment")
    sub.add_parser("reset-test", help="Reset test data")
    sub.add_parser("test", help="Full test environment restart")

    # ── Maintenance ──────────────────────────────────
    sub.add_parser("doctor", help="Run environment diagnostics")
    sub.add_parser("prune", help="Clean orphaned Docker resources")
    sub.add_parser("version", help="Show version")
    p_completion = sub.add_parser("completion", help="Generate shell completion")
    p_completion.add_argument("shell", nargs="?", choices=["bash", "zsh"], default="bash")
    p_pu = sub.add_parser("port-usage", help="Show port usage")
    p_pu.add_argument("port", nargs="?", help="port number")

    # ── IP ───────────────────────────────────────────
    p_ip = sub.add_parser("ip", help="Manage IP allocations")
    ip_sub = p_ip.add_subparsers(dest="sub")
    ip_sub.add_parser("list", help="Show allocations")
    ip_alloc = ip_sub.add_parser("alloc", help="Allocate IP to service")
    ip_alloc.add_argument("service", choices=IPVLAN_SERVICES, help="service name")
    ip_alloc.add_argument("ip", nargs="?", help="specific IP (optional)")
    ip_free = ip_sub.add_parser("free", help="Free IP from service")
    ip_free.add_argument("service", choices=IPVLAN_SERVICES, help="service name")
    ip_sub.add_parser("used", help="Show IPs in use by Docker")

    # ── Config ───────────────────────────────────────
    p_cfg = sub.add_parser("config", help="View/edit configuration")
    cfg_sub = p_cfg.add_subparsers(dest="sub")
    cfg_sub.add_parser("show", help="Show configuration")
    cfg_sub.add_parser("edit", help="Edit .env file")
    cfg_sub.add_parser("ip", help="Show IP allocations")

    return parser


def main():
    if len(sys.argv) == 1:
        cmd_dashboard()
        sys.exit(0)

    parser = build_parser()
    args = parser.parse_args()

    dispatch = {
        "start": cmd_start,
        "stop": cmd_stop,
        "teardown": cmd_teardown,
        "restart": cmd_restart,
        "reset": cmd_reset,
        "shell": cmd_shell,
        "status": cmd_status,
        "logs": cmd_logs,
        "wait": cmd_wait,
        "exec": cmd_exec,
        "init": cmd_init,
        "backup": cmd_backup,
        "list-backups": cmd_list_backups,
        "restore": cmd_restore,
        "snapshot": cmd_snapshot,
        "generate": cmd_generate,
        "list-instances": cmd_list_instances,
        "xray-start": cmd_xray_start,
        "xray-stop": cmd_xray_stop,
        "start-test": cmd_start_test,
        "stop-test": cmd_stop_test,
        "reset-test": cmd_reset_test,
        "test": cmd_test,
        "doctor": cmd_doctor,
        "prune": cmd_prune,
        "completion": cmd_completion,
        "version": cmd_version,
        "port-usage": cmd_port_usage,
        "ip": cmd_ip,
        "config": cmd_config,
    }

    fn = dispatch.get(args.cmd)
    if fn:
        fn(args)
    else:
        parser.print_help()
        sys.exit(1)


if __name__ == "__main__":
    main()
