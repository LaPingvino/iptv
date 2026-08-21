#!/usr/bin/env python3
"""
IPTV Live Bridge - Esperanto TV Library Builder
Downloads and transcodes curated Esperanto TV line-up:
- Mazi en Gondolando (Cartoon Series)
- Gerda Malaperis (Feature Film)
- Pasporto al la Tuta Mondo (Educational Series)
- Martin & la Talpoj - Superbazaro (Music Video)
- Inicialoj dc - Berlino sen vi (Music Video)
- Inicialoj dc - La fina venk' (Music Video)
"""

import os
import sys
import subprocess

TARGET_DIR = os.environ.get("BRIDGE_ESPERANTO_DIR", "/var/lib/iptv-live-bridge/esperantotv")
if not os.path.exists(TARGET_DIR):
    local_eo = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "pkg/iptv-live-bridge/esperantotv")
    os.makedirs(local_eo, exist_ok=True)
    TARGET_DIR = local_eo

MEDIA_ITEMS = [
    {
        "name": "Mazi en Gondolando",
        "tag": "mazi",
        "local": "/home/joop/Mazi en Gondolando.avi",
        "url": None
    },
    {
        "name": "Superbazaro (Martin & la Talpoj)",
        "tag": "superbazaro",
        "local": None,
        "url": "https://www.youtube.com/watch?v=gWiH8BlpU0U"
    },
    {
        "name": "Berlino sen vi (Inicialoj dc)",
        "tag": "berlinosenvi",
        "local": None,
        "url": "https://www.youtube.com/watch?v=530Y4a6jomI"
    },
    {
        "name": "La fina venk' (Inicialoj dc)",
        "tag": "lafinavenk",
        "local": None,
        "url": "https://www.youtube.com/watch?v=qJUYODkEr-o"
    },
    {
        "name": "Gerda Malaperis",
        "tag": "gerda",
        "local": None,
        "url": "https://www.youtube.com/watch?v=CdnSunTkzkk"
    },
    {
        "name": "Pasporto al la Tuta Mondo - Ep 1",
        "tag": "pasporto01",
        "local": None,
        "url": "https://www.youtube.com/watch?v=OquSnGAKYGc"
    }
]

def transcode_to_hls(input_source, tag):
    out_pattern = os.path.join(TARGET_DIR, f"{tag}_%04d.ts")
    out_m3u8 = os.path.join(TARGET_DIR, f"{tag}.m3u8")
    
    existing = [f for f in os.listdir(TARGET_DIR) if f.startswith(f"{tag}_") and f.endswith(".ts")]
    if len(existing) > 5:
        print(f"  ✓ {tag} already segmented ({len(existing)} segments). Skipping.")
        return True
        
    print(f"  ➔ Transcoding {tag} into 720p H.264 TS segments...")
    cmd = [
        "ffmpeg", "-y",
        "-i", input_source,
        "-vf", "scale=1280:720:force_original_aspect_ratio=decrease,pad=1280:720:(ow-iw)/2:(oh-ih)/2",
        "-c:v", "libx264", "-preset", "ultrafast", "-crf", "23",
        "-c:a", "aac", "-b:a", "192k", "-ar", "48000",
        "-f", "segment", "-segment_time", "6", "-segment_list", out_m3u8,
        out_pattern
    ]
    res = subprocess.run(cmd, capture_output=True, text=True)
    if res.returncode == 0:
        segs = [f for f in os.listdir(TARGET_DIR) if f.startswith(f"{tag}_") and f.endswith(".ts")]
        print(f"  ✓ Successfully created {len(segs)} segments for {tag}!")
        return True
    else:
        print(f"  ✗ Transcode failed for {tag}: {res.stderr[-200:]}")
        return False

def process_all():
    print(f"=== Building Esperanto TV Media Library in {TARGET_DIR} ===")
    for item in MEDIA_ITEMS:
        print(f"\nProcessing: {item['name']} ({item['tag']})")
        src = item["local"]
        if src and os.path.exists(src):
            transcode_to_hls(src, item["tag"])

if __name__ == "__main__":
    process_all()
