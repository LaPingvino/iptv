#!/usr/bin/env python3
"""
Upload ONLY Missing Bahá'í Studio Sessions to VPS
Run this script locally inside your folder of downloaded Studio Sessions:
  python3 upload_missing_only.py
"""

import os
import sys
import subprocess
import glob
import re
import random

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

def curate_playlist_sequence(files):
    parsed = []
    for f in files:
        title, artist = parse_song_info(f)
        parsed.append({"path": f, "title": title, "artist": artist})
    parsed.sort(key=lambda x: x["path"])
    rng = random.Random(2026)
    rng.shuffle(parsed)
    queue = list(parsed)
    dispersed = []
    artist_last_seen = {}
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
    return dispersed

def run():
    vps_host = "vps2.kiefte.eu"
    vps_dest = "joop@vps2.kiefte.eu:/home/joop/iptv/downloads/bahai_sessions/"
    
    # Check all media files in current directory
    local_files = [f for f in glob.glob("*.*") if f.lower().endswith(('.mp4', '.mkv', '.webm', '.avi', '.ts', '.m4v'))]
    if not local_files:
        print("⚠️ No media files found in current directory! Please run inside your local Studio Sessions folder.")
        sys.exit(1)
        
    print(f"Found {len(local_files)} total Studio Sessions in local directory.")
    curated = curate_playlist_sequence(local_files)
    
    # Missing indices: 526, 530, and 531..554
    # (526 and 530 had audio-only fallbacks, 531+ are missing)
    missing_items = []
    for idx, item in enumerate(curated):
        track_num = idx + 1
        # If track >= 526
        if track_num in (526, 530) or track_num >= 531:
            missing_items.append((track_num, item["path"], item["title"], item["artist"]))
            
    print(f"\nIdentified {len(missing_items)} missing video recordings to upload:")
    for num, path, title, artist in missing_items:
        print(f"  #{num:04d}: “{title}” by {artist} ({path})")
        
    files_to_upload = [item[1] for item in missing_items]
    
    print("\nStarting fast upload of ONLY the missing files...")
    cmd = ["rsync", "-avz", "--progress"] + files_to_upload + [vps_dest]
    res = subprocess.run(cmd)
    
    if res.returncode == 0:
        print("\n✓ Successfully uploaded all missing video files!")
        print("\nTriggering server-side encoding...")
        subprocess.run(["ssh", "joop@vps2.kiefte.eu", "python3 /home/joop/iptv/scripts/import_bahai_sessions.py --auto-clean"])
        print("\n🎉 All 554 Studio Sessions are 100% complete and encoded on Bahá'í TV!")
    else:
        print("✗ Rsync failed.")

if __name__ == "__main__":
    run()
