#!/usr/bin/env python3
"""
IPTV Live Bridge for Streamlink (Twitch, YouTube Live, Kick, etc.)
Transparently resolves and proxies HLS playlists (200 OK) or redirects (302),
with full support for GET, HEAD, and OPTIONS requests.
"""

import os
import sys
import time
import logging
import urllib.parse
import urllib.request
from http.server import HTTPServer, BaseHTTPRequestHandler
import streamlink

logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s [%(levelname)s] %(message)s',
    datefmt='%Y-%m-%d %H:%M:%S'
)
logger = logging.getLogger("iptv-live-bridge")

HOST = os.environ.get("BRIDGE_HOST", "0.0.0.0")
PORT = int(os.environ.get("BRIDGE_PORT", "7555"))
QUALITY = os.environ.get("BRIDGE_QUALITY", "best")
CACHE_TTL = int(os.environ.get("BRIDGE_CACHE_TTL", "15"))

# In-memory cache: url -> (resolved_url, timestamp)
stream_cache = {}

session = streamlink.Streamlink()
session.set_option("stream-timeout", 10)
session.set_option("hls-live-edge", 3)

def resolve_stream(target_url, quality=QUALITY):
    now = time.time()
    if target_url in stream_cache:
        cached_url, ts = stream_cache[target_url]
        if now - ts < CACHE_TTL:
            return cached_url
            
    logger.info(f"Resolving fresh stream for: {target_url}")
    try:
        streams = session.streams(target_url)
        if not streams:
            logger.warning(f"No streams found for {target_url} (channel may be offline)")
            return None
            
        stream_obj = None
        if quality in streams:
            stream_obj = streams[quality]
        elif "best" in streams:
            stream_obj = streams["best"]
        else:
            stream_obj = next(iter(streams.values()))
            
        stream_url = stream_obj.url
        stream_cache[target_url] = (stream_url, now)
        return stream_url
    except Exception as e:
        logger.error(f"Error resolving {target_url}: {e}")
        return None

def fetch_and_make_absolute_m3u8(m3u8_url):
    """Fetches m3u8 playlist and turns any relative segment paths into absolute URLs."""
    try:
        req = urllib.request.Request(m3u8_url, headers={"User-Agent": "Mozilla/5.0"})
        with urllib.request.urlopen(req, timeout=8) as resp:
            content = resp.read().decode("utf-8", errors="ignore")
            
        base_url = m3u8_url.rsplit("/", 1)[0] + "/"
        lines = []
        for line in content.splitlines():
            sline = line.strip()
            if sline and not sline.startswith("#"):
                if not sline.startswith("http://") and not sline.startswith("https://"):
                    lines.append(urllib.parse.urljoin(base_url, sline))
                else:
                    lines.append(sline)
            else:
                lines.append(line)
        return "\n".join(lines)
    except Exception as e:
        logger.error(f"Error proxying m3u8 content from {m3u8_url}: {e}")
        return None

class BridgeHandler(BaseHTTPRequestHandler):
    def do_OPTIONS(self):
        self.send_response(200)
        self.send_header("Access-Control-Allow-Origin", "*")
        self.send_header("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
        self.send_header("Access-Control-Allow-Headers", "*")
        self.end_headers()

    def do_HEAD(self):
        self.handle_request(is_head=True)

    def do_GET(self):
        self.handle_request(is_head=False)

    def handle_request(self, is_head=False):
        parsed = urllib.parse.urlparse(self.path)
        path = parsed.path.strip("/")
        params = urllib.parse.parse_qs(parsed.query)
        
        if path == "" or path == "health":
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Access-Control-Allow-Origin", "*")
            self.end_headers()
            if not is_head:
                self.wfile.write(b'{"status":"ok","service":"iptv-live-bridge","version":"1.2.0"}\n')
            return
            
        target_url = None
        quality = params.get("quality", [QUALITY])[0]
        use_redirect = params.get("redirect", ["0"])[0] in ["1", "true", "yes"]
        
        # Path routing
        if path.startswith("twitch/"):
            channel = path.split("/", 1)[1]
            target_url = f"https://www.twitch.tv/{channel}"
        elif path.startswith("youtube/"):
            identifier = path.split("/", 1)[1]
            if identifier.startswith("@") or identifier.startswith("channel/") or identifier.startswith("c/"):
                target_url = f"https://www.youtube.com/{identifier}/live"
            else:
                target_url = f"https://www.youtube.com/@{identifier}/live"
        elif path == "live" and "url" in params:
            target_url = params["url"][0]
            
        if not target_url:
            self.send_response(400)
            self.send_header("Content-Type", "text/plain")
            self.end_headers()
            if not is_head:
                self.wfile.write(b"400 Bad Request: Expected /twitch/<channel>, /youtube/<@handle>, or /live?url=<url>\n")
            return
            
        resolved_url = resolve_stream(target_url, quality=quality)
        if not resolved_url:
            self.send_response(404)
            self.send_header("Content-Type", "text/plain")
            self.end_headers()
            if not is_head:
                self.wfile.write(f"404 Not Found: Channel '{target_url}' is currently offline or unreachable.\n".encode("utf-8"))
            return

        if use_redirect:
            logger.info(f"Redirecting {self.path} -> {resolved_url[:80]}...")
            self.send_response(302)
            self.send_header("Location", resolved_url)
            self.send_header("Access-Control-Allow-Origin", "*")
            self.send_header("Cache-Control", "no-cache, no-store, must-revalidate")
            self.end_headers()
            return
            
        # Default: Transparent HLS Proxy (200 OK)
        m3u8_content = fetch_and_make_absolute_m3u8(resolved_url)
        if m3u8_content:
            logger.info(f"Serving 200 OK HLS playlist for {self.path} ({len(m3u8_content)} bytes)")
            self.send_response(200)
            self.send_header("Content-Type", "application/vnd.apple.mpegurl")
            self.send_header("Access-Control-Allow-Origin", "*")
            self.send_header("Cache-Control", "no-cache, no-store, must-revalidate")
            self.end_headers()
            if not is_head:
                self.wfile.write(m3u8_content.encode("utf-8"))
        else:
            # Fallback to redirect if proxy fetch fails
            self.send_response(302)
            self.send_header("Location", resolved_url)
            self.send_header("Access-Control-Allow-Origin", "*")
            self.end_headers()

    def log_message(self, format, *args):
        logger.debug("%s - - [%s] %s" % (self.address_string(), self.log_date_time_string(), format % args))

def run():
    server_address = (HOST, PORT)
    httpd = HTTPServer(server_address, BridgeHandler)
    logger.info(f"Starting IPTV Live Bridge on http://{HOST}:{PORT}")
    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        logger.info("Stopping server...")
    finally:
        httpd.server_close()

if __name__ == "__main__":
    run()
