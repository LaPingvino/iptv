#!/usr/bin/env python3
"""
IPTV Live Bridge for Streamlink (Twitch, YouTube Live, Kick, etc.)
Features:
- Transparent HLS proxying (200 OK)
- Dynamic Game Directory: /twitch/game/<game_name> (e.g. /twitch/game/Blue%20Prince, Celeste, Portal, Portal%202)
- Smart Romhack / Keyword Bias: automatically prioritizes Kaizo/Romhacks for Super Mario World
- Dynamic Live Fallback: Automatically rolls over to any active Mario/Speedrun streamer when a requested channel is offline
- Dedicated /gaming/live (Auto-Zapper) channel
- Custom offline video slate for sports / YouTube
- Full GET, HEAD, and OPTIONS support
"""

import os
import sys
import time
import json
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

ROMHACK_KEYWORDS = [
    "romhack", "hack", "kaizo", "smwc", "smwcentral", "lunar magic",
    "dram", "gauntlet", "precision", "item abuse", "shell", "blind",
    "practice", "casual romhack", "mod", "custom"
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

def get_top_streamer_for_game(game_name, bias=None):
    """Queries Twitch GraphQL for the top active broadcaster playing a specific game, with optional keyword bias."""
    raw_query = """
    query GetGameStreams($name: String!) {
      game(name: $name) {
        name
        streams(first: 25) {
          edges {
            node {
              viewersCount
              title
              freeformTags {
                name
              }
              broadcaster {
                login
                displayName
              }
            }
          }
        }
      }
    }
    """
    cleaned_name = urllib.parse.unquote(game_name).replace("-", " ")
    req = urllib.request.Request(
        "https://gql.twitch.tv/gql",
        data=json.dumps({"query": raw_query, "variables": {"name": cleaned_name}}).encode("utf-8"),
        headers={
            "Client-Id": "kimne78kx3ncx6brgo4mv6wki5h1ko",
            "Content-Type": "application/json"
        }
    )
    try:
        with urllib.request.urlopen(req, timeout=5) as resp:
            data = json.loads(resp.read().decode("utf-8"))
        game = data.get("data", {}).get("game")
        if game and game.get("streams"):
            edges = game["streams"].get("edges", [])
            
            # Check for keyword bias (e.g. romhack / kaizo for SMW)
            preferred_keywords = []
            if bias == "romhack" or ("mario" in cleaned_name.lower() and bias != "none"):
                preferred_keywords = ROMHACK_KEYWORDS
            elif bias:
                preferred_keywords = [k.strip().lower() for k in bias.split(",")]
                
            if preferred_keywords and edges:
                for edge in edges:
                    node = edge.get("node", {})
                    title = node.get("title", "").lower()
                    tags = [t.get("name", "").lower() for t in node.get("freeformTags", [])]
                    all_text = title + " " + " ".join(tags)
                    if any(kw in all_text for kw in preferred_keywords):
                        top_broadcaster = node["broadcaster"]["login"]
                        viewers = node["viewersCount"]
                        logger.info(f"Top BIAS stream for game '{cleaned_name}' ({bias}): {top_broadcaster} ({viewers} viewers) - {node['title']}")
                        return top_broadcaster
            
            # Default to #1 highest viewer count
            if edges:
                top_node = edges[0]["node"]
                top_broadcaster = top_node["broadcaster"]["login"]
                viewers = top_node["viewersCount"]
                logger.info(f"Top stream for game '{cleaned_name}': {top_broadcaster} ({viewers} viewers) - {top_node['title']}")
                return top_broadcaster
    except Exception as e:
        logger.error(f"Error querying top stream for game '{cleaned_name}': {e}")
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
                self.wfile.write(b'{"status":"ok","service":"iptv-live-bridge","version":"2.2.0"}\n')
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
        bias = params.get("bias", [None])[0]
        
        target_url = None
        is_gaming = False
        requested_channel = None
        
        # 1. Game directory auto-resolver: /twitch/game/<name> or /game/<name>
        if path.startswith("twitch/game/") or path.startswith("game/"):
            game_name = path.split("/", 2)[-1]
            top_streamer = get_top_streamer_for_game(game_name, bias=bias)
            if top_streamer:
                target_url = f"https://www.twitch.tv/{top_streamer}"
                is_gaming = True
                requested_channel = top_streamer
            else:
                logger.info(f"No active streams for game '{game_name}'. Falling back...")
                if allow_fallback:
                    fb_ch, fb_url = find_active_gaming_fallback()
                    if fb_url:
                        self.serve_hls(fb_url, is_head=is_head)
                        return
                self.serve_offline_slate(is_head=is_head)
                return

        # 2. General auto-live
        elif path in ["gaming/live", "twitch/auto-live", "twitch/live"]:
            is_gaming = True
            fallback_channel, resolved_url = find_active_gaming_fallback()
            if resolved_url:
                logger.info(f"Auto-live channel tuned into active streamer: {fallback_channel}")
                self.serve_hls(resolved_url, is_head=is_head)
                return
            else:
                self.serve_offline_slate(is_head=is_head)
                return

        # 3. Specific Twitch channel
        elif path.startswith("twitch/"):
            requested_channel = path.split("/", 1)[1]
            target_url = f"https://www.twitch.tv/{requested_channel}"
            is_gaming = True

        # 4. YouTube Live
        elif path.startswith("youtube/"):
            identifier = path.split("/", 1)[1]
            if identifier.startswith("@") or identifier.startswith("channel/") or identifier.startswith("c/"):
                target_url = f"https://www.youtube.com/{identifier}/live"
            else:
                target_url = f"https://www.youtube.com/@{identifier}/live"
            is_gaming = False

        # 5. Generic URL
        elif path == "live" and "url" in params:
            target_url = params["url"][0]
            
        if not target_url:
            self.send_response(400)
            self.send_header("Content-Type", "text/plain")
            self.end_headers()
            if not is_head:
                self.wfile.write(b"400 Bad Request: Expected /twitch/<channel>, /twitch/game/<name>, /youtube/<@handle>, or /gaming/live\n")
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
    logger.info(f"Starting IPTV Live Bridge v2.2 on http://{HOST}:{PORT}")
    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        logger.info("Stopping server...")
    finally:
        httpd.server_close()

if __name__ == "__main__":
    run()
