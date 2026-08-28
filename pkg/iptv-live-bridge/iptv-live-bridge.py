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
import datetime
import json
import logging
import subprocess
import threading
import urllib.parse
import urllib.request
from http.server import ThreadingHTTPServer, BaseHTTPRequestHandler
import queue
import collections
import signal
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
CACHE_TTL = int(os.environ.get("BRIDGE_CACHE_TTL", "300"))
MIN_VIEWER_THRESHOLD = int(os.environ.get("BRIDGE_MIN_VIEWERS", "3"))

# In-memory cache: url -> (resolved_url, timestamp)
stream_cache = {}

sys.path.insert(0, "/usr/share/iptv-live-bridge")
sys.path.insert(0, "/home/joop/iptv/src")
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from twitch_fallback import (
    GAME_GROUPS,
    CREATOR_CIRCLES,
    COMMUNITY_ADJACENT_GAMES,
    ROMHACK_KEYWORDS,
    resolve_channel_metadata,
    batch_check_streamers,
)

session = streamlink.Streamlink()
session.set_option("stream-timeout", 10)
session.set_option("hls-live-edge", 3)

OFFLINE_DIR = os.environ.get("BRIDGE_OFFLINE_DIR", "/usr/share/iptv-live-bridge/offline" if os.path.exists("/usr/share/iptv-live-bridge/offline") else os.path.join(os.path.dirname(os.path.abspath(__file__)), "offline"))
TESTCARD_DIR = os.environ.get("BRIDGE_TESTCARD_DIR", "/usr/share/iptv-live-bridge/testcard" if os.path.exists("/usr/share/iptv-live-bridge/testcard") else os.path.join(os.path.dirname(os.path.abspath(__file__)), "testcard"))
ESPERANTO_DIR = os.environ.get("BRIDGE_ESPERANTO_DIR", "/var/lib/iptv-live-bridge/esperantotv" if os.path.exists("/var/lib/iptv-live-bridge/esperantotv") else ("/usr/share/iptv-live-bridge/esperantotv" if os.path.exists("/usr/share/iptv-live-bridge/esperantotv") else os.path.join(os.path.dirname(os.path.abspath(__file__)), "esperantotv")))
BAHAI_DIR = os.environ.get("BRIDGE_BAHAI_DIR", "/var/lib/iptv-live-bridge/bahaitv" if os.path.exists("/var/lib/iptv-live-bridge/bahaitv") else ("/usr/share/iptv-live-bridge/bahaitv" if os.path.exists("/usr/share/iptv-live-bridge/bahaitv") else os.path.join(os.path.dirname(os.path.abspath(__file__)), "bahaitv")))

# BVN (Beste Van NPO) Widevine Decryption Engine with Dynamic Live Edge Buffering
BVN_DEC_KEY = "8fdccd948bb2cc6d99d5305ccffebcb7"
bvn_mpd_cache = {"url": None, "xml": None, "ts": 0, "mpd_ts": 0}

def get_bvn_stream_url():
    now = time.time()
    if bvn_mpd_cache["url"] and (now - bvn_mpd_cache["ts"]) < 1800:
        return bvn_mpd_cache["url"]
    
    last_err = None
    for attempt in range(3):
        try:
            req = urllib.request.Request(
                "https://www.bvn.tv/tv-gids/?player=live",
                headers={"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"}
            )
            html = urllib.request.urlopen(req, timeout=6).read().decode('utf-8')
            import re
            m = re.search(r'let jwtnpoplayer[a-zA-Z0-9]+\s*=\s*"([^"]+)"', html)
            if not m:
                raise RuntimeError("Could not extract BVN player token from bvn.tv")
            jwt_token = m.group(1)
            
            data = {
                "profileName": "dash",
                "drmType": "widevine",
                "referrerUrl": "https://www.bvn.tv/tv-gids/?player=live",
                "ster": {"identifier": "npo"}
            }
            req2 = urllib.request.Request(
                "https://prod.npoplayer.nl/stream-link",
                data=json.dumps(data).encode('utf-8'),
                headers={
                    "Authorization": jwt_token,
                    "Content-Type": "application/json",
                    "Origin": "https://www.bvn.tv",
                    "Referer": "https://www.bvn.tv/",
                    "User-Agent": "Mozilla/5.0"
                }
            )
            with urllib.request.urlopen(req2, timeout=6) as resp:
                res = json.loads(resp.read().decode('utf-8'))
                mpd_url = res.get('stream', {}).get('streamURL')
                if not mpd_url:
                    raise RuntimeError("No streamURL returned from npoplayer API")
                bvn_mpd_cache["url"] = mpd_url
                bvn_mpd_cache["ts"] = now
                logger.info(f"Resolved fresh BVN CDN URL: {mpd_url[:60]}...")
                return mpd_url
        except Exception as e:
            last_err = e
            time.sleep(1)
            
    if bvn_mpd_cache["url"]:
        logger.warning(f"BVN stream link refresh failed ({last_err}), using cached URL")
        return bvn_mpd_cache["url"]
    raise RuntimeError(f"Failed to resolve BVN stream after 3 attempts: {last_err}")

def get_bvn_dynamic_mpd():
    now = time.time()
    # Cache dynamic MPD for 1.5s so ffmpeg can poll continuously without CDN rate limits
    if bvn_mpd_cache["xml"] and (now - bvn_mpd_cache["mpd_ts"]) < 1.5:
        return bvn_mpd_cache["xml"]
        
    mpd_url = get_bvn_stream_url()
    mreq = urllib.request.Request(mpd_url, headers={"User-Agent": "Mozilla/5.0"})
    mpd_xml = urllib.request.urlopen(mreq, timeout=6).read().decode('utf-8')
    
    import xml.etree.ElementTree as ET
    ET.register_namespace('', 'urn:mpeg:dash:schema:mpd:2011')
    root = ET.fromstring(mpd_xml)
    root.set('suggestedPresentationDelay', 'PT15S')
    root.set('minBufferTime', 'PT15S')
    
    base_url_elem = ET.Element('{urn:mpeg:dash:schema:mpd:2011}BaseURL')
    base_url_elem.text = mpd_url.rsplit('/', 1)[0] + '/'
    root.insert(0, base_url_elem)
    
    # Filter video representations to keep only highest bitrate HD representation
    ns = {'d': 'urn:mpeg:dash:schema:mpd:2011'}
    for aset in root.findall('.//d:AdaptationSet', ns):
        if aset.get('contentType') == 'video':
            reps = aset.findall('d:Representation', ns)
            reps.sort(key=lambda r: int(r.get('bandwidth', 0)), reverse=True)
            for r in reps[1:]:
                aset.remove(r)
    
    tree = ET.ElementTree(root)
    import io
    out = io.BytesIO()
    tree.write(out, xml_declaration=True, encoding="utf-8")
    data = out.getvalue()
    bvn_mpd_cache["xml"] = data
    bvn_mpd_cache["mpd_ts"] = now
    return data

class BVNStreamEngine:
    """Shared single-instance real-time decryption broadcaster for BVN."""
    def __init__(self):
        self.proc = None
        self.lock = threading.Lock()
        self.clients = set()
        self.last_access = 0
        self.thread = None
        self.running = False
        self.recent_chunks = collections.deque(maxlen=16) # ~1MB burst buffer

    def _start(self):
        cmd = [
            "ffmpeg", "-nostdin", "-v", "warning",
            "-re",
            "-cenc_decryption_key", BVN_DEC_KEY,
            "-i", f"http://127.0.0.1:{PORT}/bvn_internal.mpd",
            "-map", "0:v:0",
            "-map", "0:a:0",
            "-c:v", "copy",
            "-bsf:v", "h264_mp4toannexb",
            "-c:a", "copy",
            "-mpegts_flags", "resend_headers+initial_discontinuity",
            "-f", "mpegts",
            "pipe:1"
        ]
        self.proc = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, bufsize=65536)
        self.running = True
        self.recent_chunks.clear()
        self.thread = threading.Thread(target=self._reader_loop, daemon=True)
        self.thread.start()
        logger.info("BVNStreamEngine started shared real-time decryption worker")

    def _reader_loop(self):
        while self.running and self.proc:
            chunk = self.proc.stdout.read(65536)
            if not chunk:
                break
            with self.lock:
                self.recent_chunks.append(chunk)
                for q in list(self.clients):
                    try:
                        q.put_nowait(chunk)
                    except queue.Full:
                        pass
                if not self.clients and (time.time() - self.last_access) > 30:
                    logger.info("No active BVN viewers for 30s, stopping background decryptor")
                    self.running = False
                    break
        with self.lock:
            if self.proc:
                try:
                    self.proc.terminate()
                    self.proc.wait()
                except Exception:
                    pass
                self.proc = None
            self.running = False

    def subscribe(self):
        q = queue.Queue(maxsize=64)
        with self.lock:
            self.last_access = time.time()
            if not self.running or self.proc is None or self.proc.poll() is not None:
                self._start()
            for c in list(self.recent_chunks):
                try:
                    q.put_nowait(c)
                except queue.Full:
                    break
            self.clients.add(q)
        return q

    def unsubscribe(self, q):
        with self.lock:
            self.clients.discard(q)
            self.last_access = time.time()

    def stop(self):
        with self.lock:
            self.running = False
            if self.proc:
                try:
                    self.proc.terminate()
                    self.proc.wait()
                except Exception:
                    pass
                self.proc = None

bvn_engine = BVNStreamEngine()

def generate_live_linear_m3u8(directory, prefix="esperanto/", standby_ts="/iptv/test/esperanto_standby0.ts", seg_duration=10.0):
    """Generates a synchronized real-time sliding-window live HLS playlist cycling 24/7 through media segments."""
    clean_prefix = prefix.strip("/")
    if not os.path.exists(directory):
        return f"#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:{int(seg_duration) + 1}\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:{seg_duration:.6f},\n{standby_ts}\n#EXT-X-ENDLIST\n"
    
    segs = sorted([f for f in os.listdir(directory) if f.endswith(".ts")])
    if not segs:
        return f"#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:{int(seg_duration) + 1}\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:{seg_duration:.6f},\n{standby_ts}\n#EXT-X-ENDLIST\n"
    
    total_segs = len(segs)
    total_cycle_time = total_segs * seg_duration
    
    current_time = time.time()
    current_offset = current_time % total_cycle_time
    current_idx = int(current_offset // seg_duration)
    media_sequence = int(current_time // seg_duration)
    
    lines = [
        "#EXTM3U",
        "#EXT-X-VERSION:3",
        f"#EXT-X-TARGETDURATION:{int(seg_duration) + 1}",
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

    # Filter candidates with >= min_viewers and exclude known AFK desktop streamers
    quality_candidates = [
        c for c in candidates 
        if c["viewers"] >= min_viewers and c["login"].lower() not in ["hercules_lostdays", "desktop"]
    ]
    
    # If no quality stream with >= min_viewers exists, return None to trigger autonomous community fallback
    if not quality_candidates:
        return None
        
    active_pool = quality_candidates

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

import threading

PRECHECKED_TWITCH_EPG_XML = ""

TWITCH_CHANNELS = [
    ("Speedrun.tv", "speedrun", "Speedrun.com 24/7"),
    ("GamesDoneQuick.tv", "gamesdonequick", "Games Done Quick"),
    ("ESAMarathon.tv", "esamarathon", "European Speedrunner Assembly"),
    ("TASVideos.tv", "tasvideos", "TASVideos"),
    ("MitchFlowerPower.tv", "mitchflowerpower", "MitchFlowerPower"),
    ("SmallAnt.tv", "smallant", "SmallAnt"),
    ("GrandPOOBear.tv", "grandpoobear", "GrandPOOBear"),
    ("SimpleFlips.tv", "simpleflips", "SimpleFlips"),
    ("Puncayshun.tv", "puncayshun", "Puncayshun"),
    ("Ryukahr.tv", "ryukahr", "Ryukahr"),
    ("PangaeaPanga.tv", "pangaeapanga", "PangaeaPanga"),
    ("CarlSagan42.tv", "carlsagan42", "CarlSagan42"),
    ("Aurateur.tv", "aurateur", "Aurateur"),
    ("HardDrop.tv", "harddrop", "Hard Drop Tetris"),
    ("ClassicTetris.tv", "classictetris", "Classic Tetris World Championship"),
    ("DGR.tv", "dgr_dave", "DGR"),
]

def twitch_background_prechecker_loop():
    """Background worker daemon that pre-checks all Twitch channels every 60s and builds static EPG."""
    global PRECHECKED_TWITCH_EPG_XML
    logger.info("Starting Twitch background prechecker daemon...")
    while True:
        try:
            now = time.time()
            start_str = datetime.datetime.fromtimestamp(now - 3600, datetime.timezone.utc).strftime("%Y%m%d%H%M%S +0000")
            stop_str = datetime.datetime.fromtimestamp(now + 3 * 3600, datetime.timezone.utc).strftime("%Y%m%d%H%M%S +0000")
            
            # Single batch GraphQL query
            queries = []
            for ch_id, login, name in TWITCH_CHANNELS:
                clean_alias = f"u_{login.lower().replace('-', '_')}"
                queries.append(f"""
                {clean_alias}: user(login: "{login.lower()}") {{
                    displayName
                    stream {{
                        title
                        viewersCount
                        game {{ name }}
                    }}
                }}
                """)
            full_query = "query BatchStreamCheck {\n" + "\n".join(queries) + "\n}"
            
            req = urllib.request.Request(
                "https://gql.twitch.tv/gql",
                data=json.dumps({"query": full_query}).encode("utf-8"),
                headers={"Client-Id": "kimne78kx3ncx6brgo4mv6wki5h1ko", "Content-Type": "application/json"}
            )
            with urllib.request.urlopen(req, timeout=5) as resp:
                data = json.loads(resp.read().decode("utf-8"))
                results = data.get("data", {})
                
            xml_lines = [
                '<?xml version="1.0" encoding="UTF-8"?>',
                '<!DOCTYPE tv SYSTEM "xmltv.dtd">',
                '<tv source-info-url="https://kiefte.eu/iptv" generator-info-name="IPTV Live Twitch Real-Time EPG Engine">'
            ]
            
            for ch_id, login, default_name in TWITCH_CHANNELS:
                xml_lines.append(f'  <channel id="{ch_id}"><display-name>{default_name}</display-name></channel>')
                meta = resolve_channel_metadata(login, default_name)
                
                title_esc = meta["epg_title"].replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")
                desc_esc = meta["epg_desc"].replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")
                cat_esc = meta["game"].replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")
                
                xml_lines.append(f'  <programme start="{start_str}" stop="{stop_str}" channel="{ch_id}">')
                xml_lines.append(f'    <title lang="en">{title_esc}</title>')
                xml_lines.append(f'    <desc lang="en">{desc_esc}</desc>')
                xml_lines.append(f'    <category lang="en">{cat_esc}</category>')
                xml_lines.append('  </programme>')
                
            xml_lines.append('</tv>')
            PRECHECKED_TWITCH_EPG_XML = "\n".join(xml_lines)
            
            # Save prechecked static EPG file to /var/lib/iptv-live-bridge/dist/twitch_epg.xml
            try:
                os.makedirs("/var/lib/iptv-live-bridge/dist", exist_ok=True)
                with open("/var/lib/iptv-live-bridge/dist/twitch_epg.xml", "w", encoding="utf-8") as f:
                    f.write(PRECHECKED_TWITCH_EPG_XML)
            except Exception:
                pass
                
        except Exception as e:
            logger.error(f"Error in Twitch background prechecker loop: {e}")
            
        time.sleep(60)

def generate_live_twitch_epg_xml():
    """Returns the pre-checked static Twitch EPG XML (instant response)."""
    global PRECHECKED_TWITCH_EPG_XML
    if PRECHECKED_TWITCH_EPG_XML:
        return PRECHECKED_TWITCH_EPG_XML
    return '<?xml version="1.0" encoding="UTF-8"?><tv><channel id="Speedrun.tv"><display-name>Speedrun.com 24/7</display-name></channel></tv>'

import shutil, concurrent.futures

DISNEY_RAM_DIR = "/run/iptv-live-bridge/disney"

class DisneyBufferEngine:
    """Pre-buffers and boosts Disney Channel PT segments using multi-threaded parallel downloads in RAM."""
    def __init__(self):
        self.mono_url = "http://151.80.18.177:86/Disney_Channel_HD/tracks-v1a1/mono.m3u8"
        self.base_url = "http://151.80.18.177:86/Disney_Channel_HD/tracks-v1a1/"
        self.last_client_access = 0
        self.running = False
        self.lock = threading.Lock()
        self.segments = {} # clean_name -> (bytes, timestamp)
        self.playlist_content = ""
        self.downloading = set()
        self.executor = None
        
    def touch(self):
        with self.lock:
            self.last_client_access = time.time()
            if not self.running:
                self.running = True
                t = threading.Thread(target=self._worker, daemon=True)
                t.start()

    def _worker(self):
        logger.info("Starting Disney Channel PT parallel pre-buffer worker...")
        os.makedirs(DISNEY_RAM_DIR, exist_ok=True)
        self.executor = concurrent.futures.ThreadPoolExecutor(max_workers=6)
        
        while self.running:
            if time.time() - self.last_client_access > 120:
                logger.info("Disney Channel idle for >2m. Stopping worker.")
                with self.lock:
                    self.running = False
                    self.segments.clear()
                    self.playlist_content = ""
                try:
                    shutil.rmtree(DISNEY_RAM_DIR, ignore_errors=True)
                except Exception:
                    pass
                break
                
            try:
                req = urllib.request.Request(self.mono_url, headers={"User-Agent": "Mozilla/5.0"})
                with urllib.request.urlopen(req, timeout=6) as resp:
                    raw_m3u8 = resp.read().decode("utf-8", errors="ignore")
                
                lines = []
                new_segs = []
                for line in raw_m3u8.splitlines():
                    sline = line.strip()
                    if sline and not sline.startswith("#"):
                        clean_name = sline.replace("/", "_")
                        lines.append(f"/iptv/disney/{clean_name}")
                        with self.lock:
                            if clean_name not in self.segments and clean_name not in self.downloading:
                                self.downloading.add(clean_name)
                                new_segs.append((sline, clean_name))
                    else:
                        lines.append(line)
                        
                with self.lock:
                    self.playlist_content = "\n".join(lines)
                    
                for rel_path, clean_name in new_segs:
                    self.executor.submit(self._fetch_and_boost, rel_path, clean_name)
                    
                now = time.time()
                with self.lock:
                    self.segments = {k: v for k, v in self.segments.items() if now - v[1] < 180}
                    
            except Exception as e:
                logger.warning(f"Disney playlist update error: {e}")
                
            time.sleep(2)
            
    def _fetch_and_boost(self, rel_path, clean_name):
        try:
            full_url = urllib.parse.urljoin(self.base_url, rel_path)
            sreq = urllib.request.Request(full_url, headers={"User-Agent": "Mozilla/5.0"})
            with urllib.request.urlopen(sreq, timeout=15) as sresp:
                raw_data = sresp.read()
                
            raw_tmp = os.path.join(DISNEY_RAM_DIR, f"raw_{clean_name}")
            boost_tmp = os.path.join(DISNEY_RAM_DIR, f"boost_{clean_name}")
            with open(raw_tmp, "wb") as f:
                f.write(raw_data)
                
            # Boost audio (+13 dB volume gain with peak limiter)
            cmd = [
                "ffmpeg", "-y", "-nostdin",
                "-i", raw_tmp,
                "-c:v", "copy",
                "-c:a", "aac", "-b:a", "192k", "-af", "volume=4.5,alimiter=limit=0.95",
                boost_tmp
            ]
            subprocess.run(cmd, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=10)
            
            final_data = None
            if os.path.exists(boost_tmp) and os.path.getsize(boost_tmp) > 1000:
                with open(boost_tmp, "rb") as f:
                    final_data = f.read()
            else:
                final_data = raw_data
                
            try:
                os.remove(raw_tmp)
                os.remove(boost_tmp)
            except Exception:
                pass
                
            with self.lock:
                self.segments[clean_name] = (final_data, time.time())
                self.downloading.discard(clean_name)
            logger.debug(f"Pre-buffered and boosted Disney segment {clean_name} ({len(final_data)} bytes)")
        except Exception as e:
            logger.warning(f"Error boosting Disney segment {clean_name}: {e}")
            with self.lock:
                self.downloading.discard(clean_name)

disney_buffer_engine = DisneyBufferEngine()

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
                self.wfile.write(b'{"status":"ok","service":"iptv-live-bridge","version":"3.7.1"}\n')
            return
            
        # Serve static distribution files (playlist.m3u8, epg.xml.gz, epg.xml, etc.)
        dist_file = path.split("/", 1)[1] if path.startswith("dist/") else path
        dist_paths = [
            os.path.join("/var/lib/iptv-live-bridge/dist", dist_file),
            os.path.join("/usr/share/iptv-live-bridge/dist", dist_file),
            os.path.join("/home/joop/iptv/dist", dist_file),
        ]
        dist_path = next((p for p in dist_paths if os.path.exists(p) and os.path.isfile(p)), None)
        if dist_path:
            self.send_response(200)
            if dist_file.endswith(".m3u8"):
                self.send_header("Content-Type", "application/vnd.apple.mpegurl")
            elif dist_file.endswith(".xml"):
                self.send_header("Content-Type", "application/xml; charset=utf-8")
            elif dist_file.endswith(".gz"):
                self.send_header("Content-Type", "application/gzip")
            elif dist_file.endswith(".json"):
                self.send_header("Content-Type", "application/json")
            else:
                self.send_header("Content-Type", "application/octet-stream")
            self.send_header("Access-Control-Allow-Origin", "*")
            self.send_header("Cache-Control", "no-cache, must-revalidate")
            self.end_headers()
            if not is_head:
                with open(dist_path, "rb") as f:
                    self.wfile.write(f.read())
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
        if path.startswith("esperanto/") or path.startswith("esperantotv/") or path in ["esperanto", "esperantotv", "esperanto.m3u8", "esperantotv.m3u8"]:
            seg_name = path.split("/", 1)[1] if "/" in path else ""
            if seg_name in ["", "tv", "live", "live.m3u8", "tv.m3u8", "index.m3u8", "playlist.m3u8", "stream.m3u8", "master.m3u8"] or path in ["esperanto", "esperantotv", "esperanto.m3u8", "esperantotv.m3u8"]:
                m3u8_content = generate_live_linear_m3u8(ESPERANTO_DIR, prefix="esperanto/", standby_ts="/iptv/test/esperanto_standby0.ts", seg_duration=10.0)
                self.send_response(200)
                self.send_header("Content-Type", "application/vnd.apple.mpegurl")
                self.send_header("Access-Control-Allow-Origin", "*")
                self.send_header("Cache-Control", "no-cache, no-store, must-revalidate")
                self.end_headers()
                if not is_head:
                    self.wfile.write(m3u8_content.encode("utf-8"))
                return
            elif seg_name in ["epg", "epg.xml"]:
                try:
                    sys.path.insert(0, "/home/joop/iptv/src")
                    from epg_generator import generate_standalone_epg_xml, ESPERANTO_METADATA
                    xml_content = generate_standalone_epg_xml("EsperantoTV.eo@SD", "Esperanto TV", ESPERANTO_DIR, ESPERANTO_METADATA)
                except Exception as e:
                    xml_content = f'<?xml version="1.0" encoding="UTF-8"?><tv><channel id="EsperantoTV.eo@SD"><display-name>Esperanto TV</display-name></channel></tv>'
                self.send_response(200)
                self.send_header("Content-Type", "application/xml; charset=utf-8")
                self.send_header("Access-Control-Allow-Origin", "*")
                self.send_header("Cache-Control", "max-age=300, must-revalidate")
                self.end_headers()
                if not is_head:
                    self.wfile.write(xml_content.encode("utf-8"))
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
                else:
                    self.send_response(404)
                    self.send_header("Content-Type", "text/plain")
                    self.send_header("Access-Control-Allow-Origin", "*")
                    self.end_headers()
                    if not is_head:
                        self.wfile.write(b"404 Not Found: Segment not found\n")
                    return

        # Serve Bahá'í Studio Sessions 24/7 Linear Broadcast stream
        if path.startswith("bahai/") or path.startswith("bahaitv/") or path in ["bahai", "bahaitv", "bahai.m3u8", "bahaitv.m3u8"]:
            seg_name = path.split("/", 1)[1] if "/" in path else ""
            if seg_name in ["", "tv", "live", "live.m3u8", "tv.m3u8", "index.m3u8", "playlist.m3u8", "stream.m3u8", "master.m3u8"] or path in ["bahai", "bahaitv", "bahai.m3u8", "bahaitv.m3u8"]:
                m3u8_content = generate_live_linear_m3u8(BAHAI_DIR, prefix="bahai/", standby_ts="/iptv/test/bahai_standby0.ts", seg_duration=8.333333)
                self.send_response(200)
                self.send_header("Content-Type", "application/vnd.apple.mpegurl")
                self.send_header("Access-Control-Allow-Origin", "*")
                self.send_header("Cache-Control", "no-cache, no-store, must-revalidate")
                self.end_headers()
                if not is_head:
                    self.wfile.write(m3u8_content.encode("utf-8"))
                return
            elif seg_name in ["epg", "epg.xml"]:
                try:
                    sys.path.insert(0, "/home/joop/iptv/src")
                    from epg_generator import generate_standalone_epg_xml
                    xml_content = generate_standalone_epg_xml("BahaiStudioSessions.tv@HD", "Bahá'í Studio Sessions TV", BAHAI_DIR, {})
                except Exception as e:
                    xml_content = f'<?xml version="1.0" encoding="UTF-8"?><tv><channel id="BahaiStudioSessions.tv@HD"><display-name>Bahá\'í Studio Sessions TV</display-name></channel></tv>'
                self.send_response(200)
                self.send_header("Content-Type", "application/xml; charset=utf-8")
                self.send_header("Access-Control-Allow-Origin", "*")
                self.send_header("Cache-Control", "max-age=300, must-revalidate")
                self.end_headers()
                if not is_head:
                    self.wfile.write(xml_content.encode("utf-8"))
                return
            else:
                seg_path = os.path.join(BAHAI_DIR, seg_name)
                if os.path.exists(seg_path):
                    self.send_response(200)
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
                        self.wfile.write(b"404 Not Found: Segment not found\n")
                    return

        # Serve dynamically modified live BVN MPD to local ffmpeg worker
        if path == "bvn_internal.mpd":
            try:
                mpd_xml = get_bvn_dynamic_mpd()
                self.send_response(200)
                self.send_header("Content-Type", "application/dash+xml")
                self.send_header("Content-Length", str(len(mpd_xml)))
                self.send_header("Cache-Control", "no-cache, must-revalidate")
                self.end_headers()
                if not is_head:
                    self.wfile.write(mpd_xml)
                return
            except Exception as e:
                logger.error(f"Error serving bvn_internal.mpd: {e}")
                self.send_response(500)
                self.end_headers()
                return

        # Serve BVN (Beste Van NPO) Live Stream (Decrypted MPEG-TS via BVNStreamEngine)
        if path.startswith("bvn") or path.startswith("nl/bvn"):
            try:
                q = bvn_engine.subscribe()
                
                self.send_response(200)
                self.send_header("Content-Type", "video/MP2T")
                self.send_header("Access-Control-Allow-Origin", "*")
                self.send_header("Connection", "close")
                self.end_headers()

                if is_head:
                    bvn_engine.unsubscribe(q)
                    return

                try:
                    while True:
                        chunk = q.get(timeout=10)
                        self.wfile.write(chunk)
                        self.wfile.flush()
                except Exception:
                    pass
                finally:
                    bvn_engine.unsubscribe(q)
                return
            except Exception as e:
                logger.error(f"Error streaming BVN: {e}")
                self.send_response(502)
                self.send_header("Content-Type", "text/plain")
                self.end_headers()
                if not is_head:
                    self.wfile.write(f"BVN stream error: {e}\n".encode("utf-8"))
                return

        # High-Speed Parallel-Buffered Disney Channel Portugal (1080p + Boosted Audio)
        if path.startswith("disney") or path in ["disney", "disney.m3u8"]:
            disney_buffer_engine.touch()
            
            if path in ["disney", "disney.m3u8", "disney/playlist.m3u8"]:
                # Wait until at least 3 boosted segments are ready in memory
                for _ in range(30):
                    with disney_buffer_engine.lock:
                        if len(disney_buffer_engine.segments) >= 3 and disney_buffer_engine.playlist_content:
                            break
                    time.sleep(0.5)
                    
                with disney_buffer_engine.lock:
                    content = disney_buffer_engine.playlist_content
                if not content:
                    content = fetch_and_make_absolute_m3u8(disney_buffer_engine.mono_url)
                    
                self.send_response(200)
                self.send_header("Content-Type", "application/vnd.apple.mpegurl")
                self.send_header("Access-Control-Allow-Origin", "*")
                self.send_header("Cache-Control", "no-cache, no-store, must-revalidate")
                self.end_headers()
                if not is_head:
                    self.wfile.write(content.encode("utf-8") if content else b"")
                return
                    
            elif path.startswith("disney/"):
                seg_name = path.split("/", 1)[1]
                # Wait up to 10s if segment is currently being fetched
                for _ in range(20):
                    with disney_buffer_engine.lock:
                        cached = disney_buffer_engine.segments.get(seg_name)
                    if cached:
                        break
                    time.sleep(0.5)
                    
                with disney_buffer_engine.lock:
                    cached = disney_buffer_engine.segments.get(seg_name)
                    
                if cached:
                    self.send_response(200)
                    self.send_header("Content-Type", "video/MP2T")
                    self.send_header("Access-Control-Allow-Origin", "*")
                    self.send_header("Cache-Control", "max-age=60, public")
                    self.end_headers()
                    if not is_head:
                        self.wfile.write(cached[0])
                    return
                else:
                    self.send_response(404)
                    self.end_headers()
                    return

        # Serve Real-Time Twitch Live EPG with actual streamer & failover metadata
        if path in ["twitch/epg", "twitch/epg.xml", "epg/twitch.xml"]:
            xml_content = generate_live_twitch_epg_xml()
            self.send_response(200)
            self.send_header("Content-Type", "application/xml; charset=utf-8")
            self.send_header("Access-Control-Allow-Origin", "*")
            self.send_header("Cache-Control", "max-age=60, must-revalidate")
            self.end_headers()
            if not is_head:
                self.wfile.write(xml_content.encode("utf-8"))
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
        self.serve_hls(resolved_url, is_head=is_head, target_url=target_url)

    def serve_hls(self, resolved_url, is_head=False, target_url=None):
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
            if target_url and target_url in stream_cache:
                del stream_cache[target_url]
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
    # Start background Twitch precheck daemon
    precheck_thread = threading.Thread(target=twitch_background_prechecker_loop, daemon=True)
    precheck_thread.start()
    
    server_address = (HOST, PORT)
    httpd = ThreadingHTTPServer(server_address, BridgeHandler)
    logger.info(f"Starting Quality Multi-Game Bridge v3.8.2 on http://{HOST}:{PORT}")
    
    def sig_handler(signum, frame):
        logger.info(f"Received signal {signum}, stopping bvn_engine and exiting cleanly...")
        bvn_engine.stop()
        httpd.server_close()
        sys.exit(0)

    signal.signal(signal.SIGTERM, sig_handler)
    signal.signal(signal.SIGINT, sig_handler)
    
    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        logger.info("Stopping server...")
    finally:
        bvn_engine.stop()
        httpd.server_close()

if __name__ == "__main__":
    run()
