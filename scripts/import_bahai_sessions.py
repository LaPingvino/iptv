#!/usr/bin/env python3
"""
IPTV Live Bridge - Bahá'í Studio Sessions Ingestion Pipeline (Curated Flow)
Batch transcodes Studio Sessions into 720p @ 30fps progressive MPEG-TS segments.
Applies a deterministic global artist & language dispersal algorithm so the 24/7 channel
flows with rich cultural, vocal, and instrumental variety.
"""

import os
import sys
import subprocess
import glob
import re
import random

DOWNLOADS_DIR = "/home/joop/iptv/downloads/bahai_sessions"
DST_DIR = "/home/joop/iptv/pkg/iptv-live-bridge/bahaitv"
os.makedirs(DST_DIR, exist_ok=True)
os.makedirs(DOWNLOADS_DIR, exist_ok=True)

def parse_song_info(filename):
    bn = os.path.splitext(os.path.basename(filename))[0]
    clean = re.sub(r'\[[a-zA-Z0-9_-]+\]', '', bn).strip()
    
    # Match "Title" by Artist
    m = re.match(r'^[“"\'‘](.*?)[”"\'’]\s*(?:by|-)?\s*(.*)', clean)
    if m:
        title = m.group(1).strip()
        artist = m.group(2).strip()
        if not artist:
            artist = "Studio Sessions"
    else:
        # Fallback if no quotes
        if " by " in clean:
            parts = clean.split(" by ", 1)
            title = parts[0].strip()
            artist = parts[1].strip()
        else:
            title = clean
            artist = "Studio Sessions"
            
    # Clean quotes from title/artist
    title = title.strip('“"\'’‘”')
    artist = artist.strip('“"\'’‘”')
    return title, artist

def sanitize_tag(title, artist):
    combined = f"{title}_{artist}"
    clean = re.sub(r'[^\w\s-]', '', combined).strip().lower()
    clean = re.sub(r'[-\s]+', '_', clean)
    return clean[:45]

def curate_playlist_sequence(files):
    """Applies deterministic seeded artist dispersal to create a natural, varied musical flow."""
    parsed = []
    for f in files:
        title, artist = parse_song_info(f)
        parsed.append({
            "path": f,
            "title": title,
            "artist": artist
        })
        
    # Sort initially by filename for a stable baseline
    parsed.sort(key=lambda x: x["path"])
    
    # Shuffle with fixed seed for determinism across builds
    rng = random.Random(2026)
    rng.shuffle(parsed)
    
    # Disperse artists to prevent repeats within 8 tracks
    dispersed = []
    artist_last_seen = {}
    queue = list(parsed)
    
    while queue:
        best_idx = 0
        for idx, item in enumerate(queue[:15]):
            last = artist_last_seen.get(item["artist"], -999)
            if len(dispersed) - last > 6:
                best_idx = idx
                break
                
        chosen = queue.pop(best_idx)
        dispersed.append(chosen)
        artist_last_seen[chosen["artist"]] = len(dispersed)
        
VAR_LIB_DIR = "/var/lib/iptv-live-bridge/bahaitv"

def transcode_video(src_path, tag, display_name, auto_clean=False):
    out_pattern = os.path.join(DST_DIR, f"{tag}_%04d.ts")
    out_m3u8 = os.path.join(DST_DIR, f"{tag}.m3u8")
    
    existing = [f for f in os.listdir(DST_DIR) if f.startswith(f"{tag}_") and f.endswith(".ts")]
    if len(existing) > 0:
        print(f"  ✓ [{tag}] already exists ({len(existing)} segments). Skipping.")
        if auto_clean and os.path.exists(src_path):
            try: os.remove(src_path)
            except Exception: pass
        return
        
    print(f"  ➔ Transcoding '{display_name}' into '{tag}' (720p @ 30fps HD)...")
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
        if auto_clean and os.path.exists(src_path):
            try:
                os.remove(src_path)
                print(f"  🧹 Cleaned temporary download '{os.path.basename(src_path)}'")
            except Exception: pass
    else:
        print(f"  ✗ Error encoding {tag}: {res.stderr[-150:]}")

def run():
    print(f"==================================================")
    print(f"  BAHÁ'Í STUDIO SESSIONS CURATED BROADCAST PIPELINE")
    print(f"==================================================")
    
    auto_clean = "--auto-clean" in sys.argv
    files = glob.glob(os.path.join(DOWNLOADS_DIR, "*.*"))
    valid_files = [f for f in files if not f.endswith(".part") and not f.endswith(".tmp") and not f.startswith(".")]
    print(f"Found {len(valid_files)} session recordings in {DOWNLOADS_DIR}")
    
    curated_list = curate_playlist_sequence(valid_files)
    print(f"Successfully generated curated flow sequence of {len(curated_list)} tracks!\n")
    
    for idx, item in enumerate(curated_list):
        tag = f"bss_{idx+1:04d}_" + sanitize_tag(item["title"], item["artist"])
        display_name = f"“{item['title']}” by {item['artist']}"
        transcode_video(item["path"], tag, display_name, auto_clean=auto_clean)

if __name__ == "__main__":
    run()
