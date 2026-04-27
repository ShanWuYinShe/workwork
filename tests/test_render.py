"""Tests for template rendering."""

from pathlib import Path
from scripts.generate_instances import render_template


class TestRenderTemplate:
    def test_simple_replacement(self, tmp_path):
        tmpl = tmp_path / "test.txt"
        tmpl.write_text("Hello {{NAME}}, port {{PORT}}")
        result = render_template(tmpl, {"NAME": "world", "PORT": 8080})
        assert result == "Hello world, port 8080"

    def test_no_vars(self, tmp_path):
        tmpl = tmp_path / "test.txt"
        tmpl.write_text("no placeholders")
        result = render_template(tmpl, {})
        assert result == "no placeholders"

    def test_list_value(self, tmp_path):
        """List values should be JSON-encoded."""
        import json
        tmpl = tmp_path / "test.json"
        tmpl.write_text('{"items": {{ITEMS}}}')
        result = render_template(tmpl, {"ITEMS": ["a", "b"]})
        assert json.loads(result) == {"items": ["a", "b"]}

    def test_xray_template_vars(self, tmp_path):
        tmpl = tmp_path / "xray.json"
        tmpl.write_text('{"id": "{{CLIENT_ID}}", "key": "{{PRIVATE_KEY}}"}')
        result = render_template(tmpl, {"CLIENT_ID": "uuid-123", "PRIVATE_KEY": "key-456"})
        assert '"uuid-123"' in result
        assert '"key-456"' in result

    def test_nginx_template_vars(self, tmp_path):
        tmpl = tmp_path / "nginx.conf"
        tmpl.write_text("proxy_pass http://{{XRAY_NAME}}:8443;\npath /{{XRAY_PATH}}/query/")
        result = render_template(tmpl, {"XRAY_NAME": "xray-reality-liyou", "XRAY_PATH": "liyou"})
        assert "http://xray-reality-liyou:8443" in result
        assert "/liyou/query/" in result
