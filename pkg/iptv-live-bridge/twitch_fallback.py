#!/usr/bin/env python3
"""
Authoritative Shared Twitch Fallback & Live Metadata Engine for IPTV.
Handles:
1. Specific Individual Streamers (with Creator Circles fallback & 'Off-air, now streaming' tag)
2. Subject / Game / Multi-Game Channels (directly resolves top live streamer with zero 'off-air')
"""

import json
import urllib.request
import urllib.parse
import logging

logger = logging.getLogger("twitch-fallback")

TWITCH_CLIENT_ID = "kimne78kx3ncx6brgo4mv6wki5h1ko"
GQL_URL = "https://gql.twitch.tv/gql"

# Multi-Game Aggregators
GAME_GROUPS = {
    "modern-tetris": ["TETR.IO", "Tetris Effect: Connected", "Tetris Effect", "TETRIS 99", "Puyo Puyo Tetris 2", "Puyo Puyo Tetris"],
    "nes-tetris": ["Tetris"],
    "mario-speedruns": ["Super Mario 64", "Super Mario World", "Super Mario Sunshine", "Super Mario Bros. 3", "Super Mario Odyssey"],
    "retro-rpg": ["Chrono Trigger", "Final Fantasy VI", "EarthBound", "Secret of Mana"]
}

# Curated Creator Affinity & Collaborator Circles (Tier 1B)
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
}

ROMHACK_KEYWORDS = [
    "kaizo", "romhack", "rom hack", "smw hack", "grand poo world", "quick boom box",
    "invictus", "learn 2 kaizo", "super dram world", "item abuse", "troll", "mario maker"
]

def query_twitch_gql(query_str, variables=None, timeout=3):
    """Executes a GraphQL request against Twitch GQL endpoint."""
    payload = {"query": query_str}
    if variables:
        payload["variables"] = variables
    data_bytes = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(
        GQL_URL,
        data=data_bytes,
        headers={"Client-Id": TWITCH_CLIENT_ID, "Content-Type": "application/json"}
    )
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return json.loads(resp.read().decode("utf-8"))
    except Exception as e:
        logger.debug(f"GQL Error: {e}")
        return None

def batch_check_streamers(logins):
    """Checks live status for multiple streamer logins in a single GQL request."""
    if not logins:
        return {}
    queries = []
    alias_map = {}
    for l in logins:
        clean = l.lower().strip()
        alias = f"u_{clean.replace('-', '_').replace('.', '_')}"
        alias_map[alias] = clean
        queries.append(f'{alias}: user(login: "{clean}") {{ displayName stream {{ title viewersCount game {{ name }} }} }}')
        
    full_query = "query BatchStreamersCheck {\n" + "\n".join(queries) + "\n}"
    res = query_twitch_gql(full_query, timeout=4)
    if not res or "data" not in res:
        return {}
        
    out = {}
    data = res["data"]
    for alias, clean_login in alias_map.items():
        user = data.get(alias)
        if user:
            s = user.get("stream")
            if s:
                out[clean_login] = {
                    "login": clean_login,
                    "display_name": user.get("displayName") or clean_login,
                    "is_live": True,
                    "title": s.get("title", ""),
                    "game": (s.get("game") or {}).get("name", "Gaming"),
                    "viewers": s.get("viewersCount", 0)
                }
            else:
                out[clean_login] = {
                    "login": clean_login,
                    "display_name": user.get("displayName") or clean_login,
                    "is_live": False,
                    "title": "",
                    "game": "Gaming",
                    "viewers": 0
                }
    return out

def query_top_streamer_for_game(game_name, bias=None):
    """Queries Twitch for the top live broadcaster under a specific game title."""
    raw_query = """
    query GetGameStreams($name: String!) {
      game(name: $name) {
        name
        streams(first: 10) {
          edges {
            node {
              viewersCount
              title
              freeformTags { name }
              broadcaster { login displayName }
            }
          }
        }
      }
    }
    """
    res = query_twitch_gql(raw_query, variables={"name": game_name}, timeout=4)
    if not res or "data" not in res or not res["data"] or not res["data"].get("game"):
        return None
        
    game_obj = res["data"]["game"]
    edges = game_obj.get("streams", {}).get("edges", [])
    if not edges:
        return None
        
    candidates = []
    preferred_keywords = ROMHACK_KEYWORDS if bias in ["romhack", "nes"] else []
    for e in edges:
        node = e.get("node", {})
        b = node.get("broadcaster", {})
        title = node.get("title", "")
        vw = node.get("viewersCount", 0)
        tags = [t.get("name", "").lower() for t in node.get("freeformTags", [])]
        all_text = (title + " " + " ".join(tags)).lower()
        has_bias = any(kw in all_text for kw in preferred_keywords) if preferred_keywords else False
        candidates.append({
            "login": b.get("login"),
            "display_name": b.get("displayName"),
            "title": title,
            "game": game_obj.get("name"),
            "viewers": vw,
            "has_bias": has_bias
        })
        
    if preferred_keywords:
        biased = [c for c in candidates if c["has_bias"]]
        if biased:
            biased.sort(key=lambda x: x["viewers"], reverse=True)
            return biased[0]
            
    candidates.sort(key=lambda x: x["viewers"], reverse=True)
    return candidates[0]

def resolve_fallback_streamer(login):
    """Derives active fallback runner using Creator Circles graph."""
    clean = login.lower().strip()
    circle = CREATOR_CIRCLES.get(clean, [])
    if not circle:
        return None
        
    candidates_data = batch_check_streamers(circle)
    for cand in circle:
        cand_info = candidates_data.get(cand)
        if cand_info and cand_info.get("is_live"):
            return cand_info
    return None

def resolve_channel_metadata(target, default_name, category_name="Gaming", url=""):
    """
    Authoritative function to resolve complete channel metadata.
    Handles both:
    1. Subject / Game / Group Channels
    2. Specific Individual Streamer Channels
    """
    raw_target = url or target
    parsed = urllib.parse.urlparse(raw_target)
    path = parsed.path.rstrip("/")
    query_params = urllib.parse.parse_qs(parsed.query)
    bias = query_params.get("bias", [None])[0]

    # --- 1. Subject / Game / Multi-Game Channels ---
    if "/game/" in path or "/group/" in path or raw_target.startswith("game:") or raw_target.startswith("group:"):
        game_names = []
        if "/game/" in path:
            game_name = urllib.parse.unquote(path.split("/game/")[-1])
            game_names = [game_name]
        elif "/group/" in path:
            group_key = path.split("/group/")[-1]
            game_names = GAME_GROUPS.get(group_key, [group_key])
            
        top_streamer = None
        for g in game_names:
            top_streamer = query_top_streamer_for_game(g, bias=bias)
            if top_streamer:
                break
                
        if top_streamer:
            dname = top_streamer["display_name"]
            gname = top_streamer["game"]
            stitle = top_streamer["title"]
            vw = top_streamer["viewers"]
            return {
                "channel_name": default_name,
                "is_live": True,
                "is_subject_channel": True,
                "active_streamer": dname,
                "game": gname,
                "title": stitle,
                "viewers": vw,
                "epg_title": f"{dname} - {gname}",
                "epg_desc": f"{stitle} (👥 {vw:,d} viewers)",
                "stream_url": f"https://www.twitch.tv/{top_streamer['login']}"
            }
        else:
            return {
                "channel_name": default_name,
                "is_live": False,
                "is_subject_channel": True,
                "active_streamer": None,
                "game": category_name,
                "title": "",
                "viewers": 0,
                "epg_title": default_name,
                "epg_desc": "Live community broadcasts when active",
                "stream_url": None
            }

    # --- 2. Specific Individual Streamer Channels ---
    login = path.split("/")[-1].split("?")[0].lower() if "/" in path else target.lower().strip()
    direct_check = batch_check_streamers([login]).get(login)
    
    if direct_check and direct_check.get("is_live"):
        return {
            "channel_name": default_name,
            "is_live": True,
            "is_subject_channel": False,
            "is_fallback": False,
            "active_streamer": direct_check["display_name"],
            "game": direct_check["game"],
            "title": direct_check["title"],
            "viewers": direct_check["viewers"],
            "epg_title": f"{direct_check['display_name']} - {direct_check['game']}",
            "epg_desc": f"{direct_check['title']} (👥 {direct_check['viewers']:,d} viewers)",
            "stream_url": f"https://www.twitch.tv/{login}"
        }
        
    # Channel is offline -> run fallback algorithm
    fallback_info = resolve_fallback_streamer(login)
    if fallback_info:
        fb_name = fallback_info["display_name"]
        fb_game = fallback_info["game"]
        fb_title = fallback_info["title"]
        fb_vw = fallback_info["viewers"]
        return {
            "channel_name": default_name,
            "is_live": True,
            "is_subject_channel": False,
            "is_fallback": True,
            "active_streamer": fb_name,
            "game": fb_game,
            "title": fb_title,
            "viewers": fb_vw,
            "epg_title": f"Off-air, now streaming {fb_name}",
            "epg_desc": f"{fb_title} - {fb_game} (👥 {fb_vw:,d} viewers)",
            "stream_url": f"https://www.twitch.tv/{fallback_info['login']}"
        }
        
    # Channel is completely off-air
    return {
        "channel_name": default_name,
        "is_live": False,
        "is_subject_channel": False,
        "is_fallback": False,
        "active_streamer": None,
        "game": category_name,
        "title": "",
        "viewers": 0,
        "epg_title": default_name,
        "epg_desc": "Off-air",
        "stream_url": None
    }
