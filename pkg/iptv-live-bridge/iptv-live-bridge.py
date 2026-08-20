#!/usr/bin/env python3
"""
IPTV Live Bridge for Streamlink (Twitch, YouTube Live, Kick, etc.)
Features:
- Transparent HLS proxying (200 OK)
- Dynamic Live Fallback: Automatically rolls over to any active Mario/Speedrun streamer when a requested channel is offline
- Dedicated /gaming/live (Auto-Zapper) channel
- Custom offline video slate for sports / YouTube
- Full GET, HEAD, and OPTIONS support
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

# Priority list for Gaming Live Fallbacks (Mario / Speedruns / GDQ)
GAMING_FALLBACK_POOL = [
    "grandpoobear",
    "thabeast721",
    "ryukahr",
    "aurateur",
    "carlsagan42",
    "elanaorama",
    "pangaeapanga",
    "simpleflips",
    "mitchflowerpower",
    "tamthegamer",
    "failstream",
    "speedrun",        # 24/7 Speedrun restream
    "esamarathon",     # 24/7 European Speedrunner Assembly
    "gamesdonequick",  # GDQ reruns & marathons
    "tasvideos"        # Tool-assisted speedruns
]

session = streamlink.Streamlink()
session.set_option("stream-timeout", 8)
session.set_option("hls-live-edge", 3)

OFFLINE_DIR = "/usr/share/iptv-live-bridge/offline" if os.path.exists("/usr/share/iptv-live-bridge/offline") else os.path.join(os.path.dirname(os.path.abspath(__file__)), "offline")

def resolve_stream(target_url, quality=QUALITY):
    now = time.time()
    if target_url in stream_cache:
        cached_url, ts = stream_cache[target_url]
        if now - ts < CACHE_TTL:
            return cached_url
            
    logger.info(f"Resolving stream: {target_url}")
    try:
        streams = session.streams(target_url)
        if not streams:
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

def find_active_gaming_fallback(exclude_channel=None):
    """Finds the first currently live stream from the curated gaming pool."""
    for channel in GAMING_FALLBACK_POOL:
        if exclude_channel and channel.lower() == exclude_channel.lower():
            continue
        url = f"https://www.twitch.tv/{channel}"
        resolved = resolve_stream(url)
        if resolved:
            logger.info(f"Found active gaming fallback: {channel}")
            return channel, resolved
    return None, None

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
                self.wfile.write(b'{"status":"ok","service":"iptv-live-bridge","version":"2.0.0"}\n')
            return
            
        # Serve local offline video segments if requested
        if path.startswith("offline/"):
            seg_name = path.split("/", 1)[1]
            seg_path = os.path.join(OFFLINE_DIR, seg_name)
            if os.path.exists(seg_path):
                self.send_response(200)
                if seg_name.endswith(".m3u8"):
                    self.send_header("Content-Type", "application/vnd.apple.mpegurl")
                else:
                    self.send_header("Content-Type", "video/MP2T")
                self.send_header("Access-Control-Allow-Origin", "*")
                self.end_headers()
                if not is_head:
                    with open(seg_path, "rb") as f:
                        self.wfile.write(f.read())
                return
            
        quality = params.get("quality", [QUALITY])[0]
        allow_fallback = params.get("fallback", ["1"])[0] in ["1", "true", "yes"]
        
        target_url = None
        is_gaming = False
        requested_channel = None
        
        if path in ["gaming/live", "twitch/auto-live", "twitch/live"]:
            is_gaming = True
            fallback_channel, resolved_url = find_active_gaming_fallback()
            if resolved_url:
                logger.info(f"Auto-live channel tuned into active streamer: {fallback_channel}")
                self.serve_hls(resolved_url, is_head=is_head)
                return
            else:
                self.serve_offline_slate(is_head=is_head)
                return
        elif path.startswith("twitch/"):
            requested_channel = path.split("/", 1)[1]
            target_url = f"https://www.twitch.tv/{requested_channel}"
            is_gaming = True
        elif path.startswith("youtube/"):
            identifier = path.split("/", 1)[1]
            if identifier.startswith("@") or identifier.startswith("channel/") or identifier.startswith("c/"):
                target_url = f"https://www.youtube.com/{identifier}/live"
            else:
                target_url = f"https://www.youtube.com/@{identifier}/live"
            is_gaming = False  # Sports/other YouTube channels don't fallback to gaming
        elif path == "live" and "url" in params:
            target_url = params["url"][0]
            
        if not target_url:
            self.send_response(400)
            self.send_header("Content-Type", "text/plain")
            self.end_headers()
            if not is_head:
                self.wfile.write(b"400 Bad Request: Expected /twitch/<channel>, /youtube/<@handle>, /gaming/live or /live?url=<url>\n")
            return
            
        resolved_url = resolve_stream(target_url, quality=quality)
        
        # If offline:
        if not resolved_url:
            if is_gaming and allow_fallback:
                logger.info(f"Stream '{requested_channel}' is OFFLINE. Searching for active gaming fallback...")
                fallback_channel, fallback_url = find_active_gaming_fallback(exclude_channel=requested_channel)
                if fallback_url:
                    logger.info(f"Redirecting offline {requested_channel} -> Live fallback: {fallback_channel}")
                    self.serve_hls(fallback_url, is_head=is_head)
                    return
            
            # Non-gaming or no live fallback available: serve offline slate
            logger.info(f"Channel '{target_url}' is OFFLINE. Serving offline video slate.")
            self.serve_offline_slate(is_head=is_head)
            return

        # Online: serve live HLS stream
        self.serve_hls(resolved_url, is_head=is_head)

    def serve_hls(self, resolved_url, is_head=False):
        m3u8_content = fetch_and_make_absolute_m3u8(resolved_url)
        if m3u8_content:
            self.send_response(200)
            self.send_header("Content-Type", "application/vnd.apple.mpegurl")
            self.send_header("Access-Control-Allow-Origin", "*")
            self.send_header("Cache-Control", "no-cache, no-store, must-revalidate")
            self.end_headers()
            if not is_head:
                self.wfile.write(m3u8_content.encode("utf-8"))
        else:
            self.send_response(302)
            self.send_header("Location", resolved_url)
            self.send_header("Access-Control-Allow-Origin", "*")
            self.end_headers()

    def serve_offline_slate(self, is_head=False):
        """Serves a continuous offline looping video playlist with absolute URLs."""
        slate_m3u8 = os.path.join(OFFLINE_DIR, "offline.m3u8")
        if os.path.exists(slate_m3u8):
            with open(slate_m3u8, "r") as f:
                content = f.read()
            # Rewrite segments to point to current host /offline/
            base_url = f"https://kiefte.eu/iptv/offline/"
            lines = []
            for line in content.splitlines():
                if line.endswith(".ts"):
                    lines.append(base_url + line.strip())
                else:
                    lines.append(line)
            slate_data = "\n".join(lines).encode("utf-8")
            
            self.send_response(200)
            self.send_header("Content-Type", "application/vnd.apple.mpegurl")
            self.send_header("Access-Control-Allow-Origin", "*")
            self.send_header("Cache-Control", "no-cache, no-store, must-revalidate")
            self.end_headers()
            if not is_head:
                self.wfile.write(slate_data)
        else:
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.end_headers()
            if not is_head:
                self.wfile.write(b"Channel is currently offline.\n")

    def log_message(self, format, *args):
        logger.debug("%s - - [%s] %s" % (self.address_string(), self.log_date_time_string(), format % args))

def run():
    server_address = (HOST, PORT)
    httpd = HTTPServer(server_address, BridgeHandler)
    logger.info(f"Starting IPTV Live Bridge v2.0 on http://{HOST}:{PORT}")
    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        logger.info("Stopping server...")
    finally:
        httpd.server_close()

if __name__ == "__main__":
    run()
