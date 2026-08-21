#!/usr/bin/env python3
"""
IPTV & Radio Playlist Generator + EPG Fetcher
Reads structured channel definitions from data/*.yaml and compiles:
- dist/playlist.m3u8 (Master playlist)
- dist/tv.m3u8 (TV-only playlist)
- dist/radio.m3u8 (Radio-only playlist)
- dist/channels.json (JSON dump)
- dist/epg.xml & dist/epg.xml.gz (Directly compatible with Sparkle TV & TiviMate)
"""

import os
import glob
import json
import yaml
import gzip
import lzma
import urllib.request

EPG_SOURCES = [
    "https://raw.githubusercontent.com/LaPingvino/iptv/main/dist/epg.xml.gz",
    "https://github.com/LITUATUI/M3UPT/raw/main/EPG/epg-m3upt.xml.xz",
    "https://raw.githubusercontent.com/Free-TV/IPTV/master/epg.xml.gz"
]

UPSTREAM_M3UPT_EPG = "https://github.com/LITUATUI/M3UPT/raw/main/EPG/epg-m3upt.xml.xz"

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
        ua = ch["http_user_agent"]
        if not ua.startswith('"'):
            ua = f'"{ua}"'
        lines.append(f'#EXTVLCOPT:http-user-agent={ua}')
    if "http_origin" in ch and ch["http_origin"]:
        lines.append(f'#EXTVLCOPT:http-origin={ch["http_origin"]}')
    if "http_referrer" in ch and ch["http_referrer"]:
        lines.append(f'#EXTVLCOPT:http-referrer={ch["http_referrer"]}')
        
    # Kodi Props
    if "kodi_props" in ch and isinstance(ch["kodi_props"], list):
        for prop in ch["kodi_props"]:
            lines.append(f'#KODIPROP:{prop}')
            
    # Stream URL
    lines.append(ch.get("url", "").strip())
    
    return "\n".join(lines)

def fetch_and_build_epg(dist_dir):
    xml_path = os.path.join(dist_dir, "epg.xml")
    gz_path = os.path.join(dist_dir, "epg.xml.gz")
    
    print("\nFetching upstream M3UPT EPG guide data & generating custom channel schedules...")
    try:
        req = urllib.request.Request(UPSTREAM_M3UPT_EPG, headers={"User-Agent": "Mozilla/5.0"})
        data_xz = urllib.request.urlopen(req, timeout=15).read()
        raw_xml = lzma.decompress(data_xz).decode("utf-8", errors="replace")
    except Exception as e:
        print(f"  ✗ Warning: Could not download upstream EPG ({e}). Initializing clean XMLTV container.")
        raw_xml = '<?xml version="1.0" encoding="UTF-8"?>\n<!DOCTYPE tv SYSTEM "xmltv.dtd">\n<tv source-info-url="https://kiefte.eu/iptv">\n</tv>'

    # Generate custom schedules via epg_generator
    try:
        from epg_generator import (
            ESPERANTO_METADATA,
            get_channel_schedule_blocks,
            generate_xmltv_programmes,
            generate_standalone_epg_xml,
            generate_twitch_epg_programmes
        )
        
        esp_media_dir = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "pkg", "iptv-live-bridge", "esperantotv")
        bah_media_dir = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "pkg", "iptv-live-bridge", "bahaitv")
        
        esp_blocks = get_channel_schedule_blocks(esp_media_dir, ESPERANTO_METADATA)
        esp_prog_xml = generate_xmltv_programmes("EsperantoTV.eo@SD", "Esperanto TV", esp_blocks)
        
        bah_blocks = get_channel_schedule_blocks(bah_media_dir)
        bah_prog_xml = generate_xmltv_programmes("BahaiStudioSessions.tv@HD", "Bahá'í Studio Sessions TV", bah_blocks)
        
        custom_channels_list = [
            '  <channel id="EsperantoTV.eo@SD">\n    <display-name>Esperanto TV</display-name>\n  </channel>',
            '  <channel id="BahaiStudioSessions.tv@HD">\n    <display-name>Bahá\'í Studio Sessions TV</display-name>\n  </channel>'
        ]
        
        twitch_progs_list = []
        # Add all Twitch gaming channels
        channels = load_channels(os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "data"))
        for ch in channels:
            if "twitch" in ch.get("url", "") and ch.get("tvg_id"):
                ch_id = ch.get("tvg_id")
                ch_name = ch.get("tvg_name") or ch.get("name")
                ch_group = ch.get("group", "Gaming")
                custom_channels_list.append(f'  <channel id="{ch_id}">\n    <display-name>{ch_name}</display-name>\n  </channel>')
                tw_prog = generate_twitch_epg_programmes(ch_id, ch_name, ch_group)
                if tw_prog:
                    twitch_progs_list.append(tw_prog)
                    
        custom_channels = "\n".join(custom_channels_list) + "\n"
        all_twitch_progs = "\n".join(twitch_progs_list)
        
        # Inject custom channels and programmes before </tv>
        if "</tv>" in raw_xml:
            parts = raw_xml.rsplit("</tv>", 1)
            merged_xml = parts[0] + "\n" + custom_channels + esp_prog_xml + "\n" + bah_prog_xml + "\n" + all_twitch_progs + "\n</tv>"
        else:
            merged_xml = raw_xml + "\n" + custom_channels + esp_prog_xml + "\n" + bah_prog_xml + "\n" + all_twitch_progs + "\n</tv>"
            
        data_xml = merged_xml.encode("utf-8")
        
        with open(xml_path, "wb") as f:
            f.write(data_xml)
            
        with gzip.open(gz_path, "wb") as f:
            f.write(data_xml)
            
        # Also write standalone EPG files
        esp_standalone = generate_standalone_epg_xml("EsperantoTV.eo@SD", "Esperanto TV", esp_media_dir, ESPERANTO_METADATA)
        with open(os.path.join(dist_dir, "esperanto_epg.xml"), "w", encoding="utf-8") as f:
            f.write(esp_standalone)
            
        print(f"  ✓ {xml_path} ({os.path.getsize(xml_path)} bytes)")
        print(f"  ✓ {gz_path} ({os.path.getsize(gz_path)} bytes)")
        print(f"  ✓ {os.path.join(dist_dir, 'esperanto_epg.xml')} ({len(esp_standalone)} bytes)")
    except Exception as e:
        print(f"  ✗ Error generating custom channel EPG: {e}")

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
        
    epg_header = f'#EXTM3U url-tvg="{",".join(EPG_SOURCES)}" x-tvg-url="{",".join(EPG_SOURCES)}"\n'
    
    master_lines = [epg_header]
    tv_lines = [epg_header]
    radio_lines = [epg_header]
    
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
    
    fetch_and_build_epg(dist_dir)

if __name__ == "__main__":
    build_playlists()
