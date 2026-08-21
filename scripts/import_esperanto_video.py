#!/usr/bin/env python3
"""
IPTV Live Bridge - Esperanto TV Media Importer
Transcodes and segments any video/movie/music video into standard 720p/1080p H.264 AAC
segments for the 24/7 continuous Esperanto TV broadcast rotation.

Usage:
  python3 import_esperanto_video.py <input_file_or_url> [prefix_tag]
"""

import sys
import os
import subprocess

TARGET_DIR = os.environ.get("BRIDGE_ESPERANTO_DIR", "/var/lib/iptv-live-bridge/esperantotv")
if not os.path.exists(TARGET_DIR):
    local_eo = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "pkg/iptv-live-bridge/esperantotv")
    if os.path.exists(local_eo):
        TARGET_DIR = local_eo
    else:
        os.makedirs(TARGET_DIR, exist_ok=True)

def import_video(input_path, prefix="video"):
    if not os.path.exists(input_path) and not input_path.startswith(("http://", "https://")):
        print(f"Error: input '{input_path}' not found.")
        sys.exit(1)
        
    print(f"=== Importing into Esperanto TV ({TARGET_DIR}) ===")
    print(f"Source: {input_path}")
    print(f"Prefix: {prefix}")
    
    out_pattern = os.path.join(TARGET_DIR, f"{prefix}_%04d.ts")
    out_m3u8 = os.path.join(TARGET_DIR, f"{prefix}.m3u8")
    
    cmd = [
        "ffmpeg", "-y",
        "-i", input_path,
        "-vf", "scale=1024:576:force_original_aspect_ratio=decrease,pad=1024:576:(ow-iw)/2:(oh-ih)/2,fps=25",
        "-c:v", "libx264", "-preset", "ultrafast", "-crf", "23", "-pix_fmt", "yuv420p",
        "-c:a", "aac", "-b:a", "128k", "-ar", "48000",
        "-f", "segment", "-segment_time", "6", "-segment_list", out_m3u8,
        out_pattern
    ]
    
    try:
        subprocess.run(cmd, check=True)
        print(f"✓ Successfully imported '{prefix}' into Esperanto TV rotation!")
    except Exception as e:
        print(f"✗ Failed to transcode '{input_path}': {e}")
        sys.exit(1)

if __name__ == "__main__":
    if len(sys.argv) < 2:
        print("Usage: python3 import_esperanto_video.py <input_file_or_url> [prefix]")
        sys.exit(1)
        
    src = sys.argv[1]
    tag = sys.argv[2] if len(sys.argv) > 2 else os.path.splitext(os.path.basename(src))[0].replace(" ", "_").lower()
    import_video(src, tag)
