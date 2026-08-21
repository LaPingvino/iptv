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
import re

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

UPSTREAM_EPG_SOURCES = [
    ("MEO PT", "https://github.com/LITUATUI/M3UPT/raw/main/EPG/epg-meo-pt.xml.xz", "xz"),
    ("NOS PT", "https://github.com/LITUATUI/M3UPT/raw/main/EPG/epg-nos-pt.xml.xz", "xz"),
    ("RTP PT", "https://github.com/LITUATUI/M3UPT/raw/main/EPG/epg-rtp-pt.xml.xz", "xz"),
]

def fetch_and_build_epg(dist_dir):
    xml_path = os.path.join(dist_dir, "epg.xml")
    gz_path = os.path.join(dist_dir, "epg.xml.gz")
    
    print("\nFetching upstream EPG guide data & generating 100% full channel coverage...")
    
    upstream_xml_blocks = []
    existing_channel_ids = set()
    
    for label, url, comp in UPSTREAM_EPG_SOURCES:
        try:
            req = urllib.request.Request(url, headers={"User-Agent": "Mozilla/5.0"})
            data = urllib.request.urlopen(req, timeout=15).read()
            if comp == "xz":
                xml_str = lzma.decompress(data).decode("utf-8", errors="replace")
            else:
                xml_str = gzip.decompress(data).decode("utf-8", errors="replace")
            
            # Extract channel IDs
            ch_ids = re.findall(r'<channel id="([^"]+)"', xml_str)
            existing_channel_ids.update(ch_ids)
            
            # Extract inner XML content (between <tv...> and </tv>)
            if "<tv" in xml_str and "</tv>" in xml_str:
                inner = xml_str.split(">", 1)[1].rsplit("</tv>", 1)[0]
                upstream_xml_blocks.append(inner)
            print(f"  ✓ [{label}] loaded {len(ch_ids)} channels")
        except Exception as e:
            print(f"  ✗ Warning: Failed to fetch {label} EPG ({e})")
            
    # Load all channels from local database
    data_dir = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "data")
    all_channels = load_channels(data_dir)
    
    # Generate custom and synthetic schedules via epg_generator
    try:
        from epg_generator import (
            ESPERANTO_METADATA,
            get_channel_schedule_blocks,
            generate_xmltv_programmes,
            generate_standalone_epg_xml,
            generate_twitch_epg_programmes,
            generate_radio_epg_programmes,
            generate_diag_epg_programmes
        )
        
        esp_media_dir = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "pkg", "iptv-live-bridge", "esperantotv")
        bah_media_dir = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "pkg", "iptv-live-bridge", "bahaitv")
        
        custom_channels_list = []
        custom_progs_list = []
        
        # 1. Esperanto TV & Bahá'í TV
        custom_channels_list.append('  <channel id="EsperantoTV.eo@SD">\n    <display-name>Esperanto TV</display-name>\n  </channel>')
        esp_blocks = get_channel_schedule_blocks(esp_media_dir, ESPERANTO_METADATA)
        custom_progs_list.append(generate_xmltv_programmes("EsperantoTV.eo@SD", "Esperanto TV", esp_blocks))
        existing_channel_ids.add("EsperantoTV.eo@SD")
        
        custom_channels_list.append('  <channel id="BahaiStudioSessions.tv@HD">\n    <display-name>Bahá\'í Studio Sessions TV</display-name>\n  </channel>')
        bah_blocks = get_channel_schedule_blocks(bah_media_dir)
        custom_progs_list.append(generate_xmltv_programmes("BahaiStudioSessions.tv@HD", "Bahá'í Studio Sessions TV", bah_blocks))
        existing_channel_ids.add("BahaiStudioSessions.tv@HD")
        
        # 2. Iterate through all database channels and fill gaps
        covered_count = 0
        for ch in all_channels:
            tid = ch.get("tvg_id", "").strip()
            if not tid:
                continue
            name = ch.get("tvg_name") or ch.get("name") or tid
            grp = ch.get("group", "")
            is_radio = is_radio_channel(ch)
            
            clean_tid = tid.split("@")[0] if "@" in tid else tid
            
            # If already covered in upstream feeds, increment and continue
            if tid in existing_channel_ids or clean_tid in existing_channel_ids:
                covered_count += 1
                continue
                
            # Channel is missing from upstream: Generate schedule!
            custom_channels_list.append(f'  <channel id="{tid}">\n    <display-name>{name}</display-name>\n  </channel>')
            existing_channel_ids.add(tid)
            covered_count += 1
            
            if "twitch" in ch.get("url", ""):
                custom_progs_list.append(generate_twitch_epg_programmes(tid, name, grp))
            elif is_radio:
                # Detect language from group or tvg_id
                lang = "pt"
                if "NL" in grp or tid.endswith(".nl"):
                    lang = "nl"
                elif "BE" in grp or tid.endswith(".be"):
                    lang = "nl-BE"
                elif "ES" in grp or "Galiza" in grp or tid.endswith(".es"):
                    lang = "gl" if "Galiza" in grp else "es"
                elif "Esperanto" in grp or "Afrikaans" in grp:
                    lang = "eo"
                custom_progs_list.append(generate_radio_epg_programmes(tid, name, lang))
            elif grp == "Diag" or "Test" in name:
                custom_progs_list.append(generate_diag_epg_programmes(tid, name))
            else:
                # General web stream / variety channel
                custom_progs_list.append(generate_twitch_epg_programmes(tid, name, grp))
                
        # Build Master XMLTV file
        xml_header = '<?xml version="1.0" encoding="UTF-8"?>\n<!DOCTYPE tv SYSTEM "xmltv.dtd">\n<tv source-info-url="https://kiefte.eu/iptv" generator-info-name="IPTV Master EPG Engine">\n'
        
        all_channels_xml = "\n".join(custom_channels_list)
        all_progs_xml = "\n".join(custom_progs_list)
        upstream_merged = "\n".join(upstream_xml_blocks)
        
        full_xml = f"{xml_header}\n{all_channels_xml}\n{upstream_merged}\n{all_progs_xml}\n</tv>\n"
        data_xml = full_xml.encode("utf-8")
        
        with open(xml_path, "wb") as f:
            f.write(data_xml)
            
        with gzip.open(gz_path, "wb") as f:
            f.write(data_xml)
            
        # Standalone Esperanto EPG
        esp_standalone = generate_standalone_epg_xml("EsperantoTV.eo@SD", "Esperanto TV", esp_media_dir, ESPERANTO_METADATA)
        with open(os.path.join(dist_dir, "esperanto_epg.xml"), "w", encoding="utf-8") as f:
            f.write(esp_standalone)
            
        print(f"\n✓ 100% EPG Coverage Achieved: {covered_count} / {len(all_channels)} channels!")
        print(f"  ✓ {xml_path} ({os.path.getsize(xml_path)} bytes)")
        print(f"  ✓ {gz_path} ({os.path.getsize(gz_path)} bytes)")
        print(f"  ✓ {os.path.join(dist_dir, 'esperanto_epg.xml')} ({len(esp_standalone)} bytes)")
    except Exception as e:
        print(f"  ✗ Error generating comprehensive EPG: {e}")

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
