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
import time
import datetime

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
    ("M3UPT MEO (PT)", "https://github.com/LITUATUI/M3UPT/raw/main/EPG/epg-meo-pt.xml.xz", "xz"),
    ("M3UPT NOS (PT)", "https://github.com/LITUATUI/M3UPT/raw/main/EPG/epg-nos-pt.xml.xz", "xz"),
    ("EPGShare Spain", "https://epgshare01.online/epgshare01/epg_ripper_ES1.xml.gz", "gz"),
    ("EPGShare Netherlands", "https://epgshare01.online/epgshare01/epg_ripper_NL1.xml.gz", "gz"),
]

def fetch_and_build_epg(dist_dir):
    xml_path = os.path.join(dist_dir, "epg.xml")
    gz_path = os.path.join(dist_dir, "epg.xml.gz")
    
    print("\nFetching official upstream EPG guide data & generating custom channel schedules...")
    
    # 1. Collect target channel IDs from our database
    data_dir = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "data")
    all_channels = load_channels(data_dir)
    target_ids = set()
    for ch in all_channels:
        tid = ch.get("tvg_id", "").strip()
        if tid:
            target_ids.add(tid)
            clean = tid.split("@")[0]
            target_ids.add(clean)
            
    extracted_channels = {}
    extracted_programmes = []
    
    # 2. Fetch and filter genuine upstream feeds
    for label, url, comp in UPSTREAM_EPG_SOURCES:
        try:
            req = urllib.request.Request(url, headers={"User-Agent": "Mozilla/5.0"})
            data = urllib.request.urlopen(req, timeout=15).read()
            if comp == "xz":
                xml_str = lzma.decompress(data).decode("utf-8", errors="replace")
            else:
                xml_str = gzip.decompress(data).decode("utf-8", errors="replace")
                
            # Extract channel tags
            ch_matches = re.findall(r'(<channel id="([^"]+)">.*?</channel>)', xml_str, re.DOTALL)
            for full_ch, ch_id in ch_matches:
                clean_id = ch_id.split("@")[0]
                if (ch_id in target_ids or clean_id in target_ids) and ch_id not in extracted_channels:
                    extracted_channels[ch_id] = full_ch
                    
            # Extract programme tags
            prog_matches = re.findall(r'(<programme [^>]*channel="([^"]+)"[^>]*>.*?</programme>)', xml_str, re.DOTALL)
            for full_prog, ch_id in prog_matches:
                clean_id = ch_id.split("@")[0]
                if ch_id in target_ids or clean_id in target_ids:
                    extracted_programmes.append(full_prog)
                    
            print(f"  ✓ [{label}] successfully processed")
        except Exception as e:
            print(f"  ✗ Warning: Could not fetch {label} ({e})")
            
    # 3. Generate deterministic schedules for local channels via epg_generator
    try:
        from epg_generator import (
            ESPERANTO_METADATA,
            get_channel_schedule_blocks,
            generate_xmltv_programmes,
            generate_standalone_epg_xml
        )
        
        esp_media_dir = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "pkg", "iptv-live-bridge", "esperantotv")
        bah_media_dir = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "pkg", "iptv-live-bridge", "bahaitv")
        
        # Esperanto TV
        extracted_channels["EsperantoTV.eo@SD"] = '  <channel id="EsperantoTV.eo@SD">\n    <display-name>Esperanto TV</display-name>\n  </channel>'
        esp_blocks = get_channel_schedule_blocks(esp_media_dir, ESPERANTO_METADATA, seg_duration=10.0)
        extracted_programmes.append(generate_xmltv_programmes("EsperantoTV.eo@SD", "Esperanto TV", esp_blocks))
        
        # Bahá'í TV
        extracted_channels["BahaiStudioSessions.tv@HD"] = '  <channel id="BahaiStudioSessions.tv@HD">\n    <display-name>Bahá\'í Studio Sessions TV</display-name>\n  </channel>'
        bah_blocks = get_channel_schedule_blocks(bah_media_dir, seg_duration=8.333333)
        extracted_programmes.append(generate_xmltv_programmes("BahaiStudioSessions.tv@HD", "Bahá'í Studio Sessions TV", bah_blocks))
        
        # 4. Add Twitch Channels with genuine live streamer info via shared library
        now = time.time()
        start_str = datetime.datetime.fromtimestamp(now - 1800, datetime.timezone.utc).strftime("%Y%m%d%H%M%S +0000")
        stop_str = datetime.datetime.fromtimestamp(now + 4 * 3600, datetime.timezone.utc).strftime("%Y%m%d%H%M%S +0000")
        
        try:
            from twitch_fallback import resolve_channel_metadata
        except ImportError:
            sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
            from twitch_fallback import resolve_channel_metadata
        
        for ch in all_channels:
            url = ch.get("url", "")
            if "twitch" in url and ch.get("tvg_id"):
                ch_id = ch.get("tvg_id")
                ch_name = ch.get("tvg_name") or ch.get("name")
                extracted_channels[ch_id] = f'  <channel id="{ch_id}">\n    <display-name>{ch_name}</display-name>\n  </channel>'
                
                login = url.rstrip("/").split("/")[-1].split("?")[0].lower()
                meta = resolve_channel_metadata(login, ch_name, ch.get("group", "Gaming"), url=url)
                
                t_esc = meta["epg_title"].replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")
                d_esc = meta["epg_desc"].replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")
                g_esc = meta["game"].replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")
                
                extracted_programmes.append(
                    f'  <programme start="{start_str}" stop="{stop_str}" channel="{ch_id}">\n'
                    f'    <title lang="en">{t_esc}</title>\n'
                    f'    <desc lang="en">{d_esc}</desc>\n'
                    f'    <category lang="en">{g_esc}</category>\n'
                    f'  </programme>'
                )
        # 5. Assemble clean XMLTV output
        xml_header = '<?xml version="1.0" encoding="UTF-8"?>\n<!DOCTYPE tv SYSTEM "xmltv.dtd">\n<tv source-info-url="https://kiefte.eu/iptv" generator-info-name="IPTV Master Curated EPG Engine">\n'
        channels_block = "\n".join(extracted_channels.values())
        programmes_block = "\n".join(extracted_programmes)
        full_xml = f"{xml_header}{channels_block}\n{programmes_block}\n</tv>\n"
        
        data_xml = full_xml.encode("utf-8")
        
        with open(xml_path, "wb") as f:
            f.write(data_xml)
            
        with gzip.open(gz_path, "wb") as f:
            f.write(data_xml)
            
        # Also write standalone Esperanto EPG file
        esp_standalone = generate_standalone_epg_xml("EsperantoTV.eo@SD", "Esperanto TV", esp_media_dir, ESPERANTO_METADATA)
        with open(os.path.join(dist_dir, "esperanto_epg.xml"), "w", encoding="utf-8") as f:
            f.write(esp_standalone)
            
        print(f"  ✓ {xml_path} ({os.path.getsize(xml_path)} bytes, {len(extracted_channels)} channels)")
        print(f"  ✓ {gz_path} ({os.path.getsize(gz_path)} bytes)")
        print(f"  ✓ {os.path.join(dist_dir, 'esperanto_epg.xml')} ({len(esp_standalone)} bytes)")
    except Exception as e:
        print(f"  ✗ Error building master EPG: {e}")
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
