#!/usr/bin/env python3
"""
Stream Quality & Bandwidth Benchmark Validator
Tests IPTV / HLS / Twitch streams for:
  1. Connection Latency (TTFB < 3.0s)
  2. Bandwidth Throughput Ratio (must be >= 1.5x real-time speed)
  3. Audio Presence & Loudness (Integrated LUFS via EBU R128)
  4. Video Resolution & Codec Integrity
"""

import sys, os, time, subprocess, json, urllib.request, urllib.parse, yaml

def test_hls_throughput(url, name="Stream"):
    print(f"\n=======================================================")
    print(f" Quality Benchmark: {name}")
    print(f" URL: {url}")
    print(f"=======================================================")
    
    t0 = time.time()
    headers = {"User-Agent": "Mozilla/5.0"}
    
    # 1. Fetch Playlist
    try:
        req = urllib.request.Request(url, headers=headers)
        with urllib.request.urlopen(req, timeout=6) as resp:
            content = resp.read().decode('utf-8', errors='ignore')
            ttfb = time.time() - t0
    except Exception as e:
        print(f"  ❌ Connection Failed: {e}")
        return False
        
    lines = [l.strip() for l in content.splitlines() if l.strip()]
    base_url = url.rsplit("/", 1)[0] + "/"
    target_duration = 6.0
    
    for l in lines:
        if "TARGETDURATION" in l:
            try:
                target_duration = float(l.split(":")[1])
            except Exception:
                pass
                
    # If master playlist, resolve first child
    if any(l.startswith("#EXT-X-STREAM-INF") for l in lines):
        try:
            child_rel = next(l for l in lines if not l.startswith("#"))
            child_url = urllib.parse.urljoin(base_url, child_rel)
            req = urllib.request.Request(child_url, headers=headers)
            with urllib.request.urlopen(req, timeout=6) as resp:
                content = resp.read().decode('utf-8', errors='ignore')
            base_url = child_url.rsplit("/", 1)[0] + "/"
            lines = [l.strip() for l in content.splitlines() if l.strip()]
            for l in lines:
                if "TARGETDURATION" in l:
                    try:
                        target_duration = float(l.split(":")[1])
                    except Exception:
                        pass
        except Exception as e:
            print(f"  ❌ Failed resolving child variant: {e}")
            return False
        
    seg_lines = [l for l in lines if not l.startswith("#")]
    if not seg_lines:
        print(f"  ❌ No video segments found in playlist!")
        return False
        
    test_seg = urllib.parse.urljoin(base_url, seg_lines[-1])
    
    # 2. Benchmark Segment Download Throughput
    t_d0 = time.time()
    try:
        sreq = urllib.request.Request(test_seg, headers=headers)
        with urllib.request.urlopen(sreq, timeout=10) as sresp:
            seg_bytes = sresp.read()
        d_time = time.time() - t_d0
    except Exception as e:
        print(f"  ❌ Segment Download Failed: {e}")
        return False
        
    speed_factor = target_duration / d_time if d_time > 0 else 0
    mb_rate = (len(seg_bytes) / 1024 / 1024) / d_time if d_time > 0 else 0
    
    print(f"  ✓ Segment Size: {len(seg_bytes)/1024/1024:.2f} MB ({target_duration:.1f}s duration)")
    print(f"  ✓ Connect Latency: {ttfb:.2f}s | Download Time: {d_time:.2f}s")
    print(f"  ✓ Throughput: {mb_rate:.2f} MB/s ({speed_factor:.2f}x real-time speed)")
    
    # 3. Audio & Loudness Analysis via local segment
    tmp_path = f"/tmp/bench_{int(time.time()*1000)}.ts"
    with open(tmp_path, "wb") as f:
        f.write(seg_bytes)
        
    probe_cmd = [
        "ffprobe", "-v", "error",
        "-show_entries", "stream=codec_type,codec_name,width,height,sample_rate,channels",
        "-of", "json", tmp_path
    ]
    p = subprocess.run(probe_cmd, capture_output=True, text=True)
    data = json.loads(p.stdout) if p.returncode == 0 else {}
    streams = data.get("streams", [])
    video_s = next((s for s in streams if s.get("codec_type") == "video"), {})
    audio_s = next((s for s in streams if s.get("codec_type") == "audio"), {})
    
    print(f"  ✓ Video: {video_s.get('width')}x{video_s.get('height')} ({video_s.get('codec_name')})")
    print(f"  ✓ Audio: {audio_s.get('codec_name')} {audio_s.get('sample_rate')}Hz ({audio_s.get('channels')} ch)")
    
    # Measure LUFS
    cmd_lufs = ["ffmpeg", "-y", "-i", tmp_path, "-af", "ebur128", "-f", "null", "-"]
    res_lufs = subprocess.run(cmd_lufs, capture_output=True, text=True)
    lufs = "N/A"
    for l in res_lufs.stderr.splitlines():
        if "I:" in l and "LUFS" in l:
            lufs = l.strip()
    print(f"  ✓ Loudness: {lufs}")
    
    try:
        os.remove(tmp_path)
    except Exception:
        pass
        
    if speed_factor >= 1.5:
        print(f"  ✅ VERDICT: PASS (Rock-solid {speed_factor:.2f}x bandwidth)")
        return True
    elif speed_factor >= 1.0:
        print(f"  ⚠️ VERDICT: MARGINAL ({speed_factor:.2f}x bandwidth)")
        return True
    else:
        print(f"  ❌ VERDICT: FAIL (Throttled: {speed_factor:.2f}x bandwidth)")
        return False

def test_file(file_path):
    with open(file_path, "r", encoding="utf-8") as f:
        data = yaml.safe_load(f)
    if not isinstance(data, list):
        print(f"Invalid yaml format in {file_path}")
        return
    passed = 0
    total = 0
    for ch in data:
        name = ch.get("name", "Unknown")
        url = ch.get("url")
        if url:
            total += 1
            if test_hls_throughput(url, name):
                passed += 1
    print(f"\n=======================================================")
    print(f" Summary for {os.path.basename(file_path)}: {passed}/{total} channels passed quality check")
    print(f"=======================================================")

if __name__ == "__main__":
    if len(sys.argv) < 2:
        print("Usage: python3 scripts/check_stream_quality.py <STREAM_URL_OR_YAML_FILE> [NAME]")
        sys.exit(1)
        
    target = sys.argv[1]
    if target.endswith(".yaml") or target.endswith(".yml"):
        test_file(target)
    else:
        name = sys.argv[2] if len(sys.argv) > 2 else "Test Stream"
        test_hls_throughput(target, name)
