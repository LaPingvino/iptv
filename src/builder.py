#!/usr/bin/env python3
"""
IPTV & Radio Playlist Generator
Reads structured channel definitions from data/*.yaml and compiles:
- dist/playlist.m3u8 (Master playlist)
- dist/tv.m3u8 (TV-only playlist)
- dist/radio.m3u8 (Radio-only playlist)
- dist/channels.json (JSON dump)
"""

import os
import glob
import json
import yaml

EPG_URL = "https://raw.githubusercontent.com/LITUATUI/M3UPT/main/EPG/m3upt.xml.xz"

def is_radio_channel(ch):
    grp = ch.get("group", "").lower()
    return "rádio" in grp or "radio" in grp or ch.get("radio", False)

def load_channels(data_dir):
    yaml_files = sorted(glob.glob(os.path.join(data_dir, "*.yaml")))
    all_channels = []
    
    for yf in yaml_files:
        with open(yf, "r", encoding="utf-8") as f:
            data = yaml.safe_load(f)
            if isinstance(data, list):
                all_channels.extend(data)
            elif isinstance(data, dict) and "channels" in data:
                all_channels.extend(data["channels"])
                
    return all_channels

def format_channel_m3u(ch):
    name = ch.get("name", "Unknown")
    group = ch.get("group", "Outros")
    tvg_id = ch.get("tvg_id", "")
    tvg_name = ch.get("tvg_name", name)
    logo = ch.get("logo", "")
    is_radio = is_radio_channel(ch)
    
    inf_parts = ['#EXTINF:-1']
    if tvg_id:
        inf_parts.append(f'tvg-id="{tvg_id}"')
    if tvg_name:
        inf_parts.append(f'tvg-name="{tvg_name}"')
    if logo:
        inf_parts.append(f'tvg-logo="{logo}"')
    if group:
        inf_parts.append(f'group-title="{group}"')
    if is_radio:
        inf_parts.append('radio="true"')
        
    extinf_line = " ".join(inf_parts) + f",{name}"
    
    lines = [extinf_line]
    
    # Headers / EXTVLCOPT
    if "http_user_agent" in ch and ch["http_user_agent"]:
        lines.append(f'#EXTVLCOPT:http-user-agent={ch["http_user_agent"]}')
    if "http_referrer" in ch and ch["http_referrer"]:
        lines.append(f'#EXTVLCOPT:http-referrer={ch["http_referrer"]}')
    if "http_origin" in ch and ch["http_origin"]:
        lines.append(f'#EXTVLCOPT:http-origin={ch["http_origin"]}')
        
    # Kodi Props
    if "kodi_props" in ch and isinstance(ch["kodi_props"], list):
        for prop in ch["kodi_props"]:
            lines.append(f'#KODIPROP:{prop}')
            
    # Stream URL
    lines.append(ch.get("url", "").strip())
    
    return "\n".join(lines)

def build_playlists():
    root_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    data_dir = os.path.join(root_dir, "data")
    dist_dir = os.path.join(root_dir, "dist")
    os.makedirs(dist_dir, exist_ok=True)
    
    channels = load_channels(data_dir)
    print(f"Loaded {len(channels)} total channels from {data_dir}")
    
    groups_count = {}
    tv_count = 0
    radio_count = 0
    for ch in channels:
        grp = ch.get("group", "Outros")
        groups_count[grp] = groups_count.get(grp, 0) + 1
        if is_radio_channel(ch):
            radio_count += 1
        else:
            tv_count += 1
        
    print("\nChannel breakdown by group:")
    for grp, count in sorted(groups_count.items()):
        print(f"  - {grp:25s}: {count:3d} channels")
        
    print(f"\nSummary: {tv_count} TV channels, {radio_count} Radio channels (Total: {len(channels)})")
        
    # 1. Master Playlist
    master_lines = [f'#EXTM3U x-tvg-url="{EPG_URL}" url-tvg="{EPG_URL}"\n']
    tv_lines = [f'#EXTM3U x-tvg-url="{EPG_URL}" url-tvg="{EPG_URL}"\n']
    radio_lines = [f'#EXTM3U x-tvg-url="{EPG_URL}" url-tvg="{EPG_URL}"\n']
    
    for ch in channels:
        entry = format_channel_m3u(ch)
        master_lines.append(entry)
        
        if is_radio_channel(ch):
            radio_lines.append(entry)
        else:
            tv_lines.append(entry)
            
    master_path = os.path.join(dist_dir, "playlist.m3u8")
    tv_path = os.path.join(dist_dir, "tv.m3u8")
    radio_path = os.path.join(dist_dir, "radio.m3u8")
    json_path = os.path.join(dist_dir, "channels.json")
    
    with open(master_path, "w", encoding="utf-8") as f:
        f.write("\n\n".join(master_lines) + "\n")
        
    with open(tv_path, "w", encoding="utf-8") as f:
        f.write("\n\n".join(tv_lines) + "\n")
        
    with open(radio_path, "w", encoding="utf-8") as f:
        f.write("\n\n".join(radio_lines) + "\n")
        
    with open(json_path, "w", encoding="utf-8") as f:
        json.dump(channels, f, indent=2, ensure_ascii=False)
        
    print(f"\nSuccessfully generated:")
    print(f"  ✓ {master_path} ({os.path.getsize(master_path)} bytes)")
    print(f"  ✓ {tv_path} ({os.path.getsize(tv_path)} bytes)")
    print(f"  ✓ {radio_path} ({os.path.getsize(radio_path)} bytes)")
    print(f"  ✓ {json_path} ({os.path.getsize(json_path)} bytes)")

if __name__ == "__main__":
    build_playlists()
