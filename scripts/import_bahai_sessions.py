#!/usr/bin/env python3
"""
IPTV Live Bridge - Bahá'í Studio Sessions Ingestion Pipeline
Batch transcodes Studio Sessions into 576p25 progressive MPEG-TS segments for 24/7 broadcast.
"""

import os
import sys
import subprocess
import glob
import re

DOWNLOADS_DIR = "/home/joop/iptv/downloads/bahai_sessions"
DST_DIR = "/home/joop/iptv/pkg/iptv-live-bridge/bahaitv"
os.makedirs(DST_DIR, exist_ok=True)
os.makedirs(DOWNLOADS_DIR, exist_ok=True)

def sanitize_tag(filename):
    clean = re.sub(r'\[[a-zA-Z0-9_-]+\]', '', filename)
    clean = re.sub(r'[^\w\s-]', '', clean).strip().lower()
    clean = re.sub(r'[-\s]+', '_', clean)
    return clean[:40]

def transcode_video(src_path, tag):
    out_pattern = os.path.join(DST_DIR, f"{tag}_%04d.ts")
    out_m3u8 = os.path.join(DST_DIR, f"{tag}.m3u8")
    
    existing = [f for f in os.listdir(DST_DIR) if f.startswith(f"{tag}_") and f.endswith(".ts")]
    if len(existing) > 0:
        print(f"  ✓ [{tag}] already exists ({len(existing)} segments). Skipping.")
        return
        
    print(f"  ➔ Transcoding '{os.path.basename(src_path)}' into '{tag}' (576p25 PAL)...")
    cmd = [
        "ffmpeg", "-y",
        "-i", src_path,
        "-vf", "scale=1024:576:force_original_aspect_ratio=decrease,pad=1024:576:(ow-iw)/2:(oh-ih)/2,fps=25",
        "-c:v", "libx264", "-preset", "ultrafast", "-crf", "23", "-pix_fmt", "yuv420p",
        "-c:a", "aac", "-b:a", "128k", "-ar", "48000",
        "-f", "segment", "-segment_time", "6", "-segment_list", out_m3u8,
        out_pattern
    ]
    res = subprocess.run(cmd, capture_output=True, text=True)
    if res.returncode == 0:
        segs = [f for f in os.listdir(DST_DIR) if f.startswith(f"{tag}_") and f.endswith(".ts")]
        print(f"  ✓ Successfully encoded {len(segs)} segments for {tag}!")
    else:
        print(f"  ✗ Error encoding {tag}: {res.stderr[-150:]}")

def run():
    print(f"=== Bahá'í Studio Sessions Batch Importer ===")
    files = glob.glob(os.path.join(DOWNLOADS_DIR, "*.*"))
    valid_files = [f for f in sorted(files) if not f.endswith(".part") and not f.endswith(".tmp")]
    print(f"Found {len(valid_files)} session recordings in {DOWNLOADS_DIR}")
    for idx, f in enumerate(valid_files):
        tag = f"bss_{idx+1:04d}_" + sanitize_tag(os.path.splitext(os.path.basename(f))[0])
        transcode_video(f, tag)

if __name__ == "__main__":
    run()
