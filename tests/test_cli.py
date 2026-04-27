"""Tests for CLI argument parsing."""

from dev.cli import build_parser

# Default services for parser construction
_SERVICES = ["postgres", "redis", "minio", "dev"]


class TestParser:
    def test_build_parser(self):
        parser = build_parser(_SERVICES)
        assert parser is not None

    def test_parse_start(self):
        parser = build_parser(_SERVICES)
        args = parser.parse_args(["start"])
        assert args.cmd == "start"

    def test_parse_up_alias(self):
        parser = build_parser(_SERVICES)
        args = parser.parse_args(["up"])
        assert args.cmd == "up"

    def test_parse_stop_all(self):
        parser = build_parser(_SERVICES)
        args = parser.parse_args(["stop", "--all"])
        assert args.cmd == "stop"
        assert args.all is True

    def test_parse_down_all(self):
        parser = build_parser(_SERVICES)
        args = parser.parse_args(["down", "--all"])
        assert args.cmd == "down"
        assert args.all is True

    def test_parse_teardown_force(self):
        parser = build_parser(_SERVICES)
        args = parser.parse_args(["teardown", "--force"])
        assert args.cmd == "teardown"
        assert args.force is True

    def test_parse_shell_type(self):
        parser = build_parser(_SERVICES)
        args = parser.parse_args(["shell", "bash"])
        assert args.shell == "bash"

    def test_parse_logs_service(self):
        parser = build_parser(_SERVICES)
        args = parser.parse_args(["logs", "postgres"])
        assert args.service == "postgres"

    def test_parse_ip_alloc(self):
        parser = build_parser(_SERVICES)
        args = parser.parse_args(["ip", "alloc", "redis"])
        assert args.cmd == "ip"
        assert args.sub == "alloc"
        assert args.service == "redis"

    def test_parse_snapshot_create(self):
        parser = build_parser(_SERVICES)
        args = parser.parse_args(["snapshot", "create", "test_snap"])
        assert args.sub == "create"
        assert args.name == "test_snap"

    def test_parse_config_show(self):
        parser = build_parser(_SERVICES)
        args = parser.parse_args(["config", "show"])
        assert args.cmd == "config"
        assert args.sub == "show"

    def test_parse_ps_alias(self):
        parser = build_parser(_SERVICES)
        args = parser.parse_args(["ps"])
        assert args.cmd == "ps"
