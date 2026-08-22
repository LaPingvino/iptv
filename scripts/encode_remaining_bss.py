#!/usr/bin/env python3
"""
IPTV Live Bridge - Final Batch Encoder for Remaining Bahá'í Studio Sessions
Encodes the 24 missing video files + video upgrades for 526 & 530 to 720p HLS segments,
hardlinks directly to /var/lib/iptv-live-bridge/bahaitv/, and auto-cleans staging downloads.
"""

import os
import sys
import subprocess
import glob
import re

DOWNLOADS_DIR = "/home/joop/iptv/downloads/bahai_sessions"
DST_DIR = "/home/joop/iptv/pkg/iptv-live-bridge/bahaitv"
VAR_LIB_DIR = "/var/lib/iptv-live-bridge/bahaitv"

def parse_song_info(filename):
    bn = os.path.splitext(os.path.basename(filename))[0]
    clean = re.sub(r'\[[a-zA-Z0-9_-]+\]', '', bn).strip()
    m = re.match(r'^[“"\'‘](.*?)[”"\'’]\s*(?:by|-)?\s*(.*)', clean)
    if m:
        title = m.group(1).strip()
        artist = m.group(2).strip() or "Studio Sessions"
    else:
        if " by " in clean:
            parts = clean.split(" by ", 1)
            title = parts[0].strip()
            artist = parts[1].strip()
        else:
            title = clean
            artist = "Studio Sessions"
    return title.strip('“"\'’‘”'), artist.strip('“"\'’‘”')

def sanitize_tag(title, artist):
    combined = f"{title}_{artist}"
    clean = re.sub(r'[^\w\s-]', '', combined).strip().lower()
    clean = re.sub(r'[-\s]+', '_', clean)
    return clean[:45]

def transcode_and_link(src_path, tag, display_name):
    out_pattern = os.path.join(DST_DIR, f"{tag}_%04d.ts")
    out_m3u8 = os.path.join(DST_DIR, f"{tag}.m3u8")
    
    # Remove existing segments (e.g. for upgrades like 526/530)
    for f in os.listdir(DST_DIR):
        if f.startswith(f"{tag}_") or f == f"{tag}.m3u8":
            try: os.remove(os.path.join(DST_DIR, f))
            except Exception: pass
    if os.path.exists(VAR_LIB_DIR):
        for f in os.listdir(VAR_LIB_DIR):
            if f.startswith(f"{tag}_") or f == f"{tag}.m3u8":
                try: os.remove(os.path.join(VAR_LIB_DIR, f))
                except Exception: pass

    print(f"  ➔ Transcoding '{display_name}' -> '{tag}' (720p @ 30fps HD)...")
    cmd = [
        "ffmpeg", "-y",
        "-i", src_path,
        "-vf", "scale=1280:720:force_original_aspect_ratio=decrease,pad=1280:720:(ow-iw)/2:(oh-ih)/2,fps=30",
        "-c:v", "libx264", "-preset", "ultrafast", "-crf", "22", "-pix_fmt", "yuv420p",
        "-c:a", "aac", "-b:a", "192k", "-ar", "48000",
        "-f", "segment", "-segment_time", "6", "-segment_list", out_m3u8,
        out_pattern
    ]
    res = subprocess.run(cmd, capture_output=True, text=True)
    if res.returncode == 0:
        segs = [f for f in os.listdir(DST_DIR) if f.startswith(f"{tag}_") and f.endswith(".ts")]
        print(f"  ✓ Successfully encoded {len(segs)} segments for {tag}!")
        if os.path.exists(VAR_LIB_DIR):
            for s in segs:
                p1 = os.path.join(DST_DIR, s)
                p2 = os.path.join(VAR_LIB_DIR, s)
                if not os.path.exists(p2):
                    try: os.link(p1, p2)
                    except Exception: pass
            m1 = os.path.join(DST_DIR, f"{tag}.m3u8")
            m2 = os.path.join(VAR_LIB_DIR, f"{tag}.m3u8")
            if os.path.exists(m1) and not os.path.exists(m2):
                try: os.link(m1, m2)
                except Exception: pass
        # Clean source download
        try:
            os.remove(src_path)
            print(f"  🧹 Cleaned staging source '{os.path.basename(src_path)}'")
        except Exception: pass
    else:
        print(f"  ✗ Error encoding {tag}: {res.stderr[-150:]}")

def run():
    print("==================================================")
    print("  BAHÁ'Í STUDIO SESSIONS FINAL CATALOG COMPLETION ")
    print("==================================================")
    
    files = sorted(glob.glob(os.path.join(DOWNLOADS_DIR, "*.*")))
    valid_files = [f for f in files if not f.endswith(".part") and not f.endswith(".tmp") and not f.startswith(".")]
    print(f"Found {len(valid_files)} uploaded session recordings in {DOWNLOADS_DIR}\n")
    
    # Process upgrades and missing files
    next_idx = 531
    for f in valid_files:
        title, artist = parse_song_info(f)
        tag_suffix = sanitize_tag(title, artist)
        display_name = f"“{title}” by {artist}"
        
        if "verily_i_say" in tag_suffix:
            tag = "bss_0526_verily_i_say_nadia_nura"
        elif "o_son_of_spirit_earl" in tag_suffix:
            tag = "bss_0530_o_son_of_spirit_earl_henrikson"
        else:
            # Check if this track is already encoded under a different index <= 525
            existing = [x for x in os.listdir(DST_DIR) if tag_suffix in x and int(x.split("_")[1]) < 526]
            if existing:
                print(f"  ℹ️ Skipping already-existing track ({existing[0].split('_')[1]}): {display_name}")
                try: os.remove(f)
                except Exception: pass
                continue
            
            tag = f"bss_{next_idx:04d}_{tag_suffix}"
            next_idx += 1
            
        transcode_and_link(f, tag, display_name)

    print("\n✓ Final batch completion finished!")
    total_tracks = len(set([x.rsplit("_", 1)[0] for x in os.listdir(DST_DIR) if x.endswith(".ts")]))
    print(f"🕊️ Total Bahá'í Studio Sessions in library now: {total_tracks} / 554 ({total_tracks/554*100:.1f}%)")

if __name__ == "__main__":
    run()
