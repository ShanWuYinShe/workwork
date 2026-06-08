import json
import os
import subprocess
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse


HOST = os.environ.get("HOST", "0.0.0.0")
PORT = int(os.environ.get("PORT", "8790"))
EXTRACTOR_BIN = os.environ.get("VOD_EXTRACTOR_BIN", "yt-dlp")
TIMEOUT_SECONDS = max(1, int(os.environ.get("VOD_EXTRACTOR_TIMEOUT_MS", "30000")) // 1000)
MAX_OUTPUT_BYTES = int(os.environ.get("VOD_EXTRACTOR_MAX_OUTPUT_BYTES", str(20 * 1024 * 1024)))
MAX_REQUEST_BYTES = 16 * 1024


class Handler(BaseHTTPRequestHandler):
    server_version = "vod-extractor/1.0"

    def do_GET(self):
        if self.path == "/health":
            self.send_json(200, {"ok": True})
            return
        self.send_json(404, {"error": "not found"})

    def do_POST(self):
        if self.path != "/extract":
            self.send_json(404, {"error": "not found"})
            return

        try:
            payload = self.read_json_body()
            url = payload.get("url")
            if not isinstance(url, str) or not is_http_url(url):
                self.send_json(400, {"error": "url must be an http(s) URL"})
                return
            extracted = extract_vod(url)
        except TimeoutError:
            self.send_json(504, {"error": "extractor timed out"})
            return
        except ValueError as error:
            self.send_json(400, {"error": str(error)})
            return
        except RuntimeError as error:
            self.send_json(502, {"error": str(error)})
            return

        self.send_json(200, extracted)

    def read_json_body(self):
        length = int(self.headers.get("content-length", "0"))
        if length <= 0:
            raise ValueError("request body is required")
        if length > MAX_REQUEST_BYTES:
            raise ValueError("request body is too large")
        raw = self.rfile.read(length)
        try:
            payload = json.loads(raw)
        except json.JSONDecodeError as error:
            raise ValueError("request body must be JSON") from error
        if not isinstance(payload, dict):
            raise ValueError("request body must be a JSON object")
        return payload

    def send_json(self, status, payload):
        body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        self.send_response(status)
        self.send_header("content-type", "application/json; charset=utf-8")
        self.send_header("content-length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, format, *args):
        print("%s - - [%s] %s" % (self.address_string(), self.log_date_time_string(), format % args))


def is_http_url(value):
    parsed = urlparse(value)
    return parsed.scheme in {"http", "https"} and bool(parsed.netloc)


def extract_vod(url):
    process = subprocess.Popen(
        [EXTRACTOR_BIN, "--dump-single-json", "--no-playlist", "--no-warnings", url],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    try:
        stdout, stderr = process.communicate(timeout=TIMEOUT_SECONDS)
    except subprocess.TimeoutExpired as error:
        process.kill()
        process.communicate()
        raise TimeoutError("extractor timed out") from error

    if len(stdout) > MAX_OUTPUT_BYTES:
        raise RuntimeError("extractor output is too large")
    if process.returncode != 0:
        message = stderr.decode("utf-8", errors="replace").strip()[:2000]
        raise RuntimeError(message or "extractor failed")

    try:
        return json.loads(stdout)
    except json.JSONDecodeError as error:
        raise RuntimeError("extractor returned invalid JSON") from error


def main():
    server = ThreadingHTTPServer((HOST, PORT), Handler)
    print(f"vod extractor listening on {HOST}:{PORT}", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
