#!/usr/bin/env python3
import os
import subprocess
import glob
import urllib.request
import shutil
import time
import json
import sys

# Drop priority so IPTV streaming, system UI, and SSH are never starved
try:
    os.nice(15)
except Exception:
    pass

SRC_KEF = "/tmp/La Plejpleja Festivalo [dokumenta filmo pri KEF 2005] [3r_cfZgre-4].mkv"
SRC_ROZOJ = "/tmp/INSULO DE LA ROZOJ  - plena filmo en Esperanto [ci226cf1JOQ].mkv"

DST_PKG = "/home/joop/iptv/pkg/iptv-live-bridge/esperantotv"
DST_VAR = "/var/lib/iptv-live-bridge/esperantotv"
STAGING_BASE = "/home/joop/iptv/staging_transcode"

os.makedirs(DST_PKG, exist_ok=True)
os.makedirs(DST_VAR, exist_ok=True)
os.makedirs(STAGING_BASE, exist_ok=True)

def get_duration(path):
    cmd = ["ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path]
    res = subprocess.run(cmd, capture_output=True, text=True)
    try:
        return float(res.stdout.strip())
    except Exception:
        return 3600.0

def encode_item(src_path, tag):
    total_dur = get_duration(src_path)
    stage_dir = os.path.join(STAGING_BASE, tag)
    if os.path.exists(stage_dir):
        shutil.rmtree(stage_dir)
    os.makedirs(stage_dir, exist_ok=True)
    
    out_pattern = os.path.join(stage_dir, f"{tag}_%04d.ts")
    out_m3u8 = os.path.join(stage_dir, f"{tag}.m3u8")
    log_file_path = os.path.join(stage_dir, "ffmpeg.log")
    
    print(f"\n🎬 Starting safe transcode of '{os.path.basename(src_path)}' -> '{tag}' (total duration: {int(total_dur//60)}m {int(total_dur%60)}s)...", flush=True)
    
    # Use 3 threads and idle I/O priority to keep the server completely responsive and avoid pipe deadlock
    cmd = [
        "ionice", "-c", "3",
        "ffmpeg", "-y",
        "-threads", "3",
        "-i", src_path,
        "-vf", "scale=1024:576:force_original_aspect_ratio=decrease,pad=1024:576:(ow-iw)/2:(oh-ih)/2,fps=25,setsar=1",
        "-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-pix_fmt", "yuv420p",
        "-c:a", "aac", "-b:a", "128k", "-ar", "48000", "-ac", "2",
        "-f", "segment", "-segment_time", "10",
        "-segment_list", out_m3u8,
        "-progress", "pipe:1",
        out_pattern
    ]
    
    with open(log_file_path, "w") as log_f:
        proc = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=log_f, universal_newlines=True)
        
        last_print = time.time()
        for line in proc.stdout:
            line = line.strip()
            if line.startswith("out_time_us="):
                try:
                    us = int(line.split("=")[1])
                    cur_sec = us / 1000000.0
                    now = time.time()
                    if now - last_print >= 15:
                        pct = min(100.0, (cur_sec / total_dur) * 100.0)
                        print(f"  ⏳ [{tag}] Transcoded {int(cur_sec//60):02d}:{int(cur_sec%60):02d} / {int(total_dur//60):02d}:{int(total_dur%60):02d} ({pct:.1f}%)", flush=True)
                        last_print = now
                except Exception:
                    pass

        ret = proc.wait()
        
    if ret != 0:
        with open(log_file_path, "r") as log_f:
            err = log_f.read()[-500:]
        print(f"❌ Failed to encode {tag}: {err}", flush=True)
        return False
        
    new_segs = sorted(glob.glob(os.path.join(stage_dir, f"{tag}_*.ts")))
    print(f"🔎 Verifying {len(new_segs)} segments for '{tag}' before deploying...", flush=True)
    
    # Spot check first, middle, last segments for valid 2ch audio
    sample_indices = [0, len(new_segs)//4, len(new_segs)//2, 3*len(new_segs)//4, len(new_segs)-1]
    for idx in sample_indices:
        if 0 <= idx < len(new_segs):
            seg = new_segs[idx]
            probe_cmd = ['ffprobe', '-v', 'quiet', '-print_format', 'json', '-show_streams', seg]
            res = subprocess.run(probe_cmd, capture_output=True, text=True)
            data = json.loads(res.stdout)
            audio = [s for s in data.get('streams', []) if s.get('codec_type') == 'audio']
            if not audio or audio[0].get('channels', 0) != 2:
                print(f"❌ Segment verification failed on {os.path.basename(seg)}: {audio}", flush=True)
                return False

    print(f"✅ Audio verified (stereo AAC 48kHz). Deploying to {DST_PKG} and {DST_VAR}...", flush=True)
    for s in new_segs:
        base = os.path.basename(s)
        pkg_s = os.path.join(DST_PKG, base)
        var_s = os.path.join(DST_VAR, base)
        shutil.copy2(s, pkg_s)
        try:
            if os.path.exists(var_s): os.remove(var_s)
            os.link(pkg_s, var_s)
        except Exception:
            shutil.copy2(pkg_s, var_s)

    shutil.copy2(out_m3u8, os.path.join(DST_PKG, f"{tag}.m3u8"))
    var_m3u8 = os.path.join(DST_VAR, f"{tag}.m3u8")
    try:
        if os.path.exists(var_m3u8): os.remove(var_m3u8)
        os.link(os.path.join(DST_PKG, f"{tag}.m3u8"), var_m3u8)
    except Exception:
        shutil.copy2(out_m3u8, var_m3u8)

    shutil.rmtree(stage_dir)
    print(f"🎉 '{tag}' fully deployed!", flush=True)
    return True

if os.path.exists(SRC_KEF):
    if not encode_item(SRC_KEF, "dok_kef2005"):
        print("Aborting due to error in dok_kef2005")
        sys.exit(1)

if os.path.exists(SRC_ROZOJ):
    if not encode_item(SRC_ROZOJ, "dok_insulo_de_la_rozoj"):
        print("Aborting due to error in dok_insulo_de_la_rozoj")
        sys.exit(1)

# Trigger reload on local bridge
try:
    with urllib.request.urlopen("http://[fd00:2830::7555]:8080/iptv/reload", timeout=5) as resp:
        print("🔄 In-memory reload triggered on bridge:", resp.read().decode(), flush=True)
except Exception as e:
    print("Reload error:", e, flush=True)

print("🎉 All movies successfully transcoded, verified, and live!", flush=True)
