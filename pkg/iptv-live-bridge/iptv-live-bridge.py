#!/usr/bin/env python3
"""
IPTV Live Bridge for Streamlink (Twitch, YouTube Live, Kick, etc.) - v3.4.0
Smart Quality-Filtered, Multi-Game & Social/Community-First IPTV Live Gateway.

Features:
- Transparent HLS proxying (200 OK)
- Quality & Minimum Viewer Threshold: Ignores 1-viewer AFK/desktop screens and seeks real active broadcasts
- Multi-Game Aggregator: /twitch/games/<g1>+<g2> or /twitch/group/modern-tetris
- Curated Champion & Tournament Priority for Classic Tetris
- 5-Tier Autonomous Social/Community Fallback Engine
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
MIN_VIEWER_THRESHOLD = int(os.environ.get("BRIDGE_MIN_VIEWERS", "3"))

# In-memory cache: url -> (resolved_url, timestamp)
stream_cache = {}

# Predefined Multi-Game Groups
GAME_GROUPS = {
    "modern-tetris": ["TETR.IO", "Tetris Effect: Connected", "Tetris Effect", "TETRIS 99", "Puyo Puyo Tetris 2", "Puyo Puyo Tetris"],
    "nes-tetris": ["Tetris"],
    "mario-speedruns": ["Super Mario 64", "Super Mario World", "Super Mario Sunshine", "Super Mario Bros. 3", "Super Mario Odyssey"],
    "retro-rpg": ["Chrono Trigger", "Final Fantasy VI", "EarthBound", "Secret of Mana"]
}

# Well-known Creator Affinity & Collaborator Circles (Tier 1B)
CREATOR_CIRCLES = {
    "classictetris": ["dogplayingtetris", "fractal", "ericicx", "alex_t", "bluescuti", "classictetris2", "classictetris3", "classictetris4", "harddrop", "wumbotize", "doremy"],
    "classictetris2": ["classictetris", "dogplayingtetris", "fractal", "ericicx", "alex_t", "bluescuti", "classictetris3", "classictetris4"],
    "classictetris3": ["classictetris", "dogplayingtetris", "fractal", "ericicx", "alex_t", "bluescuti", "classictetris2", "classictetris4"],
    "classictetris4": ["classictetris", "dogplayingtetris", "fractal", "ericicx", "alex_t", "bluescuti", "classictetris2", "classictetris3"],
    "vinesandwillows": ["wumbotize", "doremy", "harddrop", "dogplayingtetris", "fractal", "carrarium", "ambercyprian", "smallant", "speedrun"],
    "wumbotize": ["doremy", "harddrop", "dogplayingtetris", "fractal", "speedrun"],
    "doremy": ["wumbotize", "harddrop", "dogplayingtetris", "fractal", "speedrun"],
    "harddrop": ["wumbotize", "doremy", "dogplayingtetris", "fractal", "classictetris", "speedrun"],
    "dogplayingtetris": ["fractal", "ericicx", "alex_t", "bluescuti", "classictetris", "wumbotize"],
    "ericicx": ["dogplayingtetris", "fractal", "alex_t", "bluescuti", "classictetris"],
    "fractal": ["dogplayingtetris", "ericicx", "alex_t", "bluescuti", "classictetris"],
    "alex_t": ["dogplayingtetris", "fractal", "ericicx", "bluescuti", "classictetris"],
    "bluescuti": ["dogplayingtetris", "fractal", "ericicx", "alex_t", "classictetris"],
    "ryukahr": ["tamthegamer", "dgr_dave", "smallant", "thabeast721", "aurateur", "grandpoobear"],
    "tamthegamer": ["ryukahr", "elanaorama", "smallant", "dgr_dave"],
    "carlsagan42": ["juzcook", "dgr_dave", "grandpoobear", "thabeast721", "aurateur"],
    "juzcook": ["carlsagan42", "dgr_dave", "grandpoobear", "thabeast721", "pangaeapanga"],
    "dgr_dave": ["smallant", "carlsagan42", "juzcook", "ryukahr", "thabeast721"],
    "smallant": ["dgr_dave", "ryukahr", "speedrun", "thabeast721", "grandpoobear"],
    "elanaorama": ["smallant", "tamthegamer", "ryukahr", "speedrun"],
    "thabeast721": ["grandpoobear", "aurateur", "pangaeapanga", "simpleflips", "carlsagan42", "glitchcat7"],
    "grandpoobear": ["thabeast721", "aurateur", "carlsagan42", "juzcook", "pangaeapanga", "glitchcat7"],
    "glitchcat7": ["thabeast721", "grandpoobear", "carlsagan42", "juzcook", "aurateur", "pangaeapanga", "speedrun"],
    "aurateur": ["thabeast721", "grandpoobear", "carlsagan42", "pangaeapanga", "speedrun"],
    "pangaeapanga": ["thabeast721", "grandpoobear", "aurateur", "juzcook", "simpleflips"],
    "simpleflips": ["thabeast721", "grandpoobear", "smallant", "carlsagan42"],
    "mitchflowerpower": ["thabeast721", "grandpoobear", "speedrun", "aurateur"],
    "failstream": ["carlsagan42", "juzcook", "aurateur", "grandpoobear"],
    "carrarium": ["tgh_sr", "ambercyprian", "msushi", "speedrun", "gamesdonequick", "esamarathon"],
    "tgh_sr": ["carrarium", "ambercyprian", "msushi", "speedrun", "gamesdonequick", "esamarathon"],
    "ambercyprian": ["tgh_sr", "carrarium", "msushi", "speedrun"],
    "msushi": ["carrarium", "tgh_sr", "ambercyprian", "speedrun"],
    "gamesdonequick": ["esamarathon", "speedrun", "tasvideos"],
    "esamarathon": ["speedrun", "gamesdonequick", "tasvideos"],
    "speedrun": ["esamarathon", "gamesdonequick", "tasvideos"],
    "tasvideos": ["speedrun", "esamarathon", "gamesdonequick"]
}

# Community-Adjacent Game Graph (Tier 3)
COMMUNITY_ADJACENT_GAMES = {
    "super mario maker 2": ["super mario world", "super mario 64", "super mario bros. 3", "retro"],
    "super mario world": ["super mario maker 2", "super mario 64", "super mario bros. 3", "retro"],
    "super mario 64": ["super mario sunshine", "super mario galaxy", "super mario world", "retro"],
    "blue prince": ["outer wilds", "the witness", "animal well", "myst", "puzzle"],
    "celeste": ["super meat boy", "hollow knight", "pizza tower", "retro"],
    "portal": ["portal 2", "the talos principle", "half-life 2"],
    "portal 2": ["portal", "the talos principle", "half-life 2"],
    "tetris": ["tetr.io", "tetris effect: connected", "tetris effect", "tetris 99", "retro"],
    "tetr.io": ["tetris effect: connected", "tetris", "puyo puyo tetris 2"],
    "planet coaster 2": ["planet coaster", "rollercoaster tycoon 2", "cities: skylines ii", "colony survival"],
    "darkest dungeon": ["darkest dungeon ii", "slay the spire", "hades ii", "roguelike"],
    "metroid prime origins": ["metroid prime", "super metroid", "metroid dread"],
    "zeepkist": ["trackmania", "marble it up!", "trials rising", "retro"],
    "trackmania": ["zeepkist", "trials rising", "retro"],
    "chess": ["tabletop simulator", "retro"],
    "geoguessr": ["retro"]
}

ROMHACK_KEYWORDS = [
    "romhack", "hack", "kaizo", "smwc", "smwcentral", "lunar magic",
    "dram", "gauntlet", "precision", "item abuse", "shell", "blind",
    "practice", "casual romhack", "mod", "custom", "nes", "rolling", "hypertap", "ctwc"
]

session = streamlink.Streamlink()
session.set_option("stream-timeout", 8)
session.set_option("hls-live-edge", 3)

OFFLINE_DIR = os.environ.get("BRIDGE_OFFLINE_DIR", "/usr/share/iptv-live-bridge/offline" if os.path.exists("/usr/share/iptv-live-bridge/offline") else os.path.join(os.path.dirname(os.path.abspath(__file__)), "offline"))
TESTCARD_DIR = os.environ.get("BRIDGE_TESTCARD_DIR", "/usr/share/iptv-live-bridge/testcard" if os.path.exists("/usr/share/iptv-live-bridge/testcard") else os.path.join(os.path.dirname(os.path.abspath(__file__)), "testcard"))
ESPERANTO_DIR = os.environ.get("BRIDGE_ESPERANTO_DIR", "/var/lib/iptv-live-bridge/esperantotv" if os.path.exists("/var/lib/iptv-live-bridge/esperantotv") else ("/usr/share/iptv-live-bridge/esperantotv" if os.path.exists("/usr/share/iptv-live-bridge/esperantotv") else os.path.join(os.path.dirname(os.path.abspath(__file__)), "esperantotv")))

def generate_live_linear_m3u8(directory, prefix="esperanto/"):
    """Generates a synchronized real-time sliding-window live HLS playlist cycling 24/7 through media segments."""
    clean_prefix = prefix.strip("/")
    if not os.path.exists(directory):
        return f"#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:6\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:6.000000,\n/iptv/test/esperanto_standby0.ts\n#EXT-X-ENDLIST\n"
    
    segs = sorted([f for f in os.listdir(directory) if f.endswith(".ts")])
    if not segs:
        return f"#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:6\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:6.000000,\n/iptv/test/esperanto_standby0.ts\n#EXT-X-ENDLIST\n"
    
    seg_duration = 6.0
    total_segs = len(segs)
    total_cycle_time = total_segs * seg_duration
    
    current_time = time.time()
    current_offset = current_time % total_cycle_time
    current_idx = int(current_offset // seg_duration)
    media_sequence = int(current_time // seg_duration)
    
    lines = [
        "#EXTM3U",
        "#EXT-X-VERSION:3",
        f"#EXT-X-TARGETDURATION:{int(seg_duration)}",
        f"#EXT-X-MEDIA-SEQUENCE:{media_sequence}"
    ]
    
    for k in range(5):
        seg_i = (current_idx + k) % total_segs
        lines.append(f"#EXTINF:{seg_duration:.6f},")
        lines.append(f"/iptv/{clean_prefix}/{segs[seg_i]}")
        
    return "\n".join(lines) + "\n"

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

def get_top_streamer_across_multi_games(game_names, bias=None, exclude_login=None, min_viewers=MIN_VIEWER_THRESHOLD):
    """Queries Twitch across multiple game titles in parallel, filtering out 1-viewer AFK desktop streams."""
    candidates = []
    preferred_keywords = ROMHACK_KEYWORDS if bias in ["romhack", "nes"] else []
    if bias and bias not in ["romhack", "nes", "none"]:
        preferred_keywords = [k.strip().lower() for k in bias.split(",")]
        
    for game_name in game_names:
        raw_query = """
        query GetGameStreams($name: String!) {
          game(name: $name) {
            name
            streams(first: 20) {
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
            headers={"Client-Id": "kimne78kx3ncx6brgo4mv6wki5h1ko", "Content-Type": "application/json"}
        )
        try:
            with urllib.request.urlopen(req, timeout=4) as resp:
                data = json.loads(resp.read().decode("utf-8"))
            game = data.get("data", {}).get("game")
            if game and game.get("streams"):
                for edge in game["streams"].get("edges", []):
                    node = edge.get("node", {})
                    b_login = node.get("broadcaster", {}).get("login")
                    if exclude_login and b_login and b_login.lower() == exclude_login.lower():
                        continue
                    
                    viewers = node.get("viewersCount", 0)
                    # Filter out low-quality/AFK 1-viewer streams unless no other option
                    title = node.get("title", "").lower()
                    tags = [t.get("name", "").lower() for t in node.get("freeformTags", [])]
                    all_text = title + " " + " ".join(tags)
                    has_bias = any(kw in all_text for kw in preferred_keywords) if preferred_keywords else False
                    
                    candidates.append({
                        "login": b_login,
                        "viewers": viewers,
                        "title": node.get("title", ""),
                        "game": game.get("name"),
                        "has_bias": has_bias
                    })
        except Exception as e:
            logger.debug(f"Error querying multi-game {game_name}: {e}")

    if not candidates:
        return None

    # Filter candidates with >= min_viewers if any exist
    quality_candidates = [c for c in candidates if c["viewers"] >= min_viewers]
    active_pool = quality_candidates if quality_candidates else candidates

    # If bias given and matching quality streams exist
    if preferred_keywords:
        biased = [c for c in active_pool if c["has_bias"]]
        if biased:
            biased.sort(key=lambda x: x["viewers"], reverse=True)
            top = biased[0]
            logger.info(f"Top Multi-Game BIAS Stream ({bias}): {top['login']} ({top['viewers']} viewers | {top['game']}) - {top['title']}")
            return top["login"]

    active_pool.sort(key=lambda x: x["viewers"], reverse=True)
    top = active_pool[0]
    logger.info(f"Top Multi-Game Stream: {top['login']} ({top['viewers']} viewers | {top['game']}) - {top['title']}")
    return top["login"]

def find_autonomous_fallback_for_channel(channel_login):
    """
    Tiered social & community discovery for ANY offline channel on Twitch:
    1. Tier 1A: Check Official Twitch Team live members
    2. Tier 1B: Check Creator Social/Friend Circles
    3. Tier 2: Query streamer's last played game category (with Romhack bias & viewer threshold)
    4. Tier 3: Query community-adjacent game categories (Modern Tetris, SMW, etc.)
    5. Tier 4: Speedrun.com 24/7 restream
    """
    req_clean = channel_login.lower().strip()
    
    # Tier 1B Check: Creator Social & Friend Circle (Fast local check)
    if req_clean in CREATOR_CIRCLES:
        for friend in CREATOR_CIRCLES[req_clean]:
            url = f"https://www.twitch.tv/{friend}"
            resolved = resolve_stream(url)
            if resolved:
                logger.info(f"Tier 1B (Social Circle) fallback for {req_clean} -> {friend}")
                return friend, resolved

    # GraphQL Query for User context & primaryTeam
    raw_query = """
    query GetUserContext($login: String!) {
      user(login: $login) {
        id
        login
        displayName
        broadcastSettings {
          title
          game {
            id
            name
          }
        }
        primaryTeam {
          name
          displayName
          members {
            edges {
              node {
                login
                displayName
                stream {
                  id
                  viewersCount
                }
              }
            }
          }
        }
      }
    }
    """
    try:
        req = urllib.request.Request(
            "https://gql.twitch.tv/gql",
            data=json.dumps({"query": raw_query, "variables": {"login": req_clean}}).encode("utf-8"),
            headers={"Client-Id": "kimne78kx3ncx6brgo4mv6wki5h1ko", "Content-Type": "application/json"}
        )
        with urllib.request.urlopen(req, timeout=5) as resp:
            data = json.loads(resp.read().decode("utf-8"))
        user = data.get("data", {}).get("user")
    except Exception as e:
        logger.error(f"Error querying Twitch GQL context for {channel_login}: {e}")
        user = None

    # Tier 1A: Official Twitch Team live members
    if user and user.get("primaryTeam"):
        team = user["primaryTeam"]
        members = team.get("members", {}).get("edges", [])
        for m in members:
            node = m.get("node", {})
            m_login = node.get("login", "")
            if m_login and m_login.lower() != req_clean and node.get("stream"):
                url = f"https://www.twitch.tv/{m_login}"
                resolved = resolve_stream(url)
                if resolved:
                    logger.info(f"Tier 1A (Twitch Team '{team.get('displayName')}') fallback for {req_clean} -> {m_login}")
                    return m_login, resolved

    # Tier 2: Same Game Category (with viewer threshold)
    if user:
        bs = user.get("broadcastSettings", {})
        game = bs.get("game")
        game_name = game.get("name") if game else None
        if game_name:
            logger.info(f"Tier 2 search: {channel_login} normally broadcasts '{game_name}'. Searching live streams...")
            top_live_in_game = get_top_streamer_across_multi_games([game_name], exclude_login=req_clean, min_viewers=3)
            if top_live_in_game:
                url = f"https://www.twitch.tv/{top_live_in_game}"
                resolved = resolve_stream(url)
                if resolved:
                    logger.info(f"Tier 2 (Same Game) fallback for {req_clean} -> {top_live_in_game} ({game_name})")
                    return top_live_in_game, resolved
                    
            # Tier 3: Community-adjacent games
            g_clean = game_name.lower().strip()
            adjacent_list = COMMUNITY_ADJACENT_GAMES.get(g_clean, [])
            if adjacent_list:
                top_adj = get_top_streamer_across_multi_games(adjacent_list, exclude_login=req_clean, min_viewers=3)
                if top_adj:
                    url = f"https://www.twitch.tv/{top_adj}"
                    resolved = resolve_stream(url)
                    if resolved:
                        logger.info(f"Tier 3 (Community-Adjacent Game) fallback for {req_clean} -> {top_adj}")
                        return top_adj, resolved

    # Tier 4: Speedrun.com 24/7 restream
    fallback_url = "https://www.twitch.tv/speedrun"
    resolved = resolve_stream(fallback_url)
    if resolved:
        return "speedrun", resolved
        
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
        if path.startswith("iptv/"):
            path = path[5:].strip("/")
        params = urllib.parse.parse_qs(parsed.query)
        
        if path == "" or path == "health":
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Access-Control-Allow-Origin", "*")
            self.end_headers()
            if not is_head:
                self.wfile.write(b'{"status":"ok","service":"iptv-live-bridge","version":"3.6.0"}\n')
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
            
        # Serve diagnostic testcard & HDR switching segments if requested
        if path.startswith("testcard/") or path.startswith("test/"):
            seg_name = path.split("/", 1)[1] if "/" in path else ""
            if seg_name in ["", "avsync", "pattern", "ipv6"]:
                seg_name = "testcard.m3u8"
            elif seg_name in ["hdr", "hlg", "hdr10", "hdr_switch", "hdr12"]:
                seg_name = "hdr_switch.m3u8"
            elif seg_name in ["hdr-smooth", "smooth", "hdr30", "hdr_smooth", "relaxed"]:
                seg_name = "hdr_smooth.m3u8"
                
            seg_path = os.path.join(TESTCARD_DIR, seg_name)
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
            else:
                self.send_response(404)
                self.send_header("Content-Type", "text/plain")
                self.send_header("Access-Control-Allow-Origin", "*")
                self.end_headers()
                if not is_head:
                    self.wfile.write(b"Test asset not found\n")
                return
            
        # Serve Esperanto TV 24/7 Linear Broadcast stream
        if path.startswith("esperanto/") or path.startswith("esperantotv/") or path == "esperanto":
            seg_name = path.split("/", 1)[1] if "/" in path else ""
            if seg_name in ["", "tv", "live", "live.m3u8", "tv.m3u8", "index.m3u8"]:
                m3u8_content = generate_live_linear_m3u8(ESPERANTO_DIR, prefix="esperanto/")
                self.send_response(200)
                self.send_header("Content-Type", "application/vnd.apple.mpegurl")
                self.send_header("Access-Control-Allow-Origin", "*")
                self.send_header("Cache-Control", "no-cache, no-store, must-revalidate")
                self.end_headers()
                if not is_head:
                    self.wfile.write(m3u8_content.encode("utf-8"))
                return
            else:
                seg_path = os.path.join(ESPERANTO_DIR, seg_name)
                if os.path.exists(seg_path):
                    self.send_response(200)
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
        
        # 1. Multi-game Group resolver: /twitch/group/<group_name> or /twitch/games/<g1>+<g2>
        if path.startswith("twitch/group/") or path.startswith("group/"):
            group_name = path.split("/", 2)[-1].lower()
            games_list = GAME_GROUPS.get(group_name, [group_name])
            top_streamer = get_top_streamer_across_multi_games(games_list, bias=bias, min_viewers=3)
            if top_streamer:
                target_url = f"https://www.twitch.tv/{top_streamer}"
                is_gaming = True
                requested_channel = top_streamer
            else:
                if allow_fallback:
                    fb_ch, fb_url = find_autonomous_fallback_for_channel(group_name)
                    if fb_url:
                        self.serve_hls(fb_url, is_head=is_head)
                        return
                self.serve_offline_slate(is_head=is_head)
                return
                
        elif path.startswith("twitch/games/") or path.startswith("games/"):
            raw_games = path.split("/", 2)[-1]
            games_list = [urllib.parse.unquote(g).strip() for g in raw_games.replace("+", ",").split(",")]
            top_streamer = get_top_streamer_across_multi_games(games_list, bias=bias, min_viewers=3)
            if top_streamer:
                target_url = f"https://www.twitch.tv/{top_streamer}"
                is_gaming = True
                requested_channel = top_streamer
            else:
                self.serve_offline_slate(is_head=is_head)
                return

        # 2. Single Game directory auto-resolver: /twitch/game/<name>
        elif path.startswith("twitch/game/") or path.startswith("game/"):
            game_name = path.split("/", 2)[-1]
            if game_name.lower() in GAME_GROUPS:
                games_list = GAME_GROUPS[game_name.lower()]
                top_streamer = get_top_streamer_across_multi_games(games_list, bias=bias, min_viewers=3)
            else:
                top_streamer = get_top_streamer_across_multi_games([game_name], bias=bias, min_viewers=3)
                
            if top_streamer:
                target_url = f"https://www.twitch.tv/{top_streamer}"
                is_gaming = True
                requested_channel = top_streamer
            else:
                logger.info(f"No active streams for game '{game_name}'. Falling back...")
                if allow_fallback:
                    fb_ch, fb_url = find_autonomous_fallback_for_channel(game_name)
                    if fb_url:
                        self.serve_hls(fb_url, is_head=is_head)
                        return
                self.serve_offline_slate(is_head=is_head)
                return

        # 3. General auto-live
        elif path in ["gaming/live", "twitch/auto-live", "twitch/live"]:
            is_gaming = True
            top_speedrun = resolve_stream("https://www.twitch.tv/speedrun")
            if top_speedrun:
                self.serve_hls(top_speedrun, is_head=is_head)
                return
            else:
                self.serve_offline_slate(is_head=is_head)
                return

        # 4. Specific Twitch channel
        elif path.startswith("twitch/"):
            requested_channel = path.split("/", 1)[1]
            target_url = f"https://www.twitch.tv/{requested_channel}"
            is_gaming = True

        # 5. YouTube Live
        elif path.startswith("youtube/"):
            identifier = path.split("/", 1)[1]
            if identifier.startswith("@") or identifier.startswith("channel/") or identifier.startswith("c/"):
                target_url = f"https://www.youtube.com/{identifier}/live"
            else:
                target_url = f"https://www.youtube.com/@{identifier}/live"
            is_gaming = False

        # 6. Generic URL
        elif path == "live" and "url" in params:
            target_url = params["url"][0]
            
        if not target_url:
            self.send_response(400)
            self.send_header("Content-Type", "text/plain")
            self.end_headers()
            if not is_head:
                self.wfile.write(b"400 Bad Request: Expected /twitch/<channel>, /twitch/game/<name>, /twitch/group/<name>, /youtube/<@handle>, or /gaming/live\n")
            return
            
        resolved_url = resolve_stream(target_url, quality=quality)
        
        # If offline:
        if not resolved_url:
            if is_gaming and allow_fallback and requested_channel:
                logger.info(f"Stream '{requested_channel}' is OFFLINE. Discovering team/friend/community live streams...")
                fallback_channel, fallback_url = find_autonomous_fallback_for_channel(requested_channel)
                if fallback_url:
                    logger.info(f"Routing offline {requested_channel} -> Quality Social/Community fallback: {fallback_channel}")
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
    logger.info(f"Starting Quality Multi-Game Bridge v3.4 on http://{HOST}:{PORT}")
    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        logger.info("Stopping server...")
    finally:
        httpd.server_close()

if __name__ == "__main__":
    run()
