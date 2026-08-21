#!/usr/bin/env python3
"""
IPTV Live Bridge - Relaxed/Smooth HDR Dynamic Switching Suite (30-second Cadence)
Encodes the Sintel 1080p master into 30-second segments with a 4-stage graceful curve:
  Stage 0: SDR Baseline (BT.709, Gamma 2.4, 100 nits)
  Stage 1: HLG Broadcast HDR (BT.2100 ARIB-B67, 1000 nits)
  Stage 2: HDR10 Peak Dynamic Range (SMPTE ST 2084 PQ, BT.2020, 1000 nits)
  Stage 3: HLG Step-Down (Soft transition to prevent decoder shock before SDR)
"""

import os
import sys
import subprocess
import time

SOURCE_VIDEO = "/tmp/sintel_1080p.webm"
OUT_DIR = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "pkg/iptv-live-bridge/testcard")
os.makedirs(OUT_DIR, exist_ok=True)

SEG_DURATION = 30.0

def get_duration(video_path):
    cmd = ["ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", video_path]
    res = subprocess.run(cmd, capture_output=True, text=True)
    return float(res.stdout.strip())

def render_smooth_suite():
    if not os.path.exists(SOURCE_VIDEO):
        print(f"Error: {SOURCE_VIDEO} not found.")
        sys.exit(1)
        
    total_dur = get_duration(SOURCE_VIDEO)
    total_segs = int(total_dur // SEG_DURATION)
    print(f"=== Rendering Smooth HDR Suite (30s Cadence) ===")
    print(f"Total Duration: {total_dur:.1f}s across {total_segs} segments of {SEG_DURATION}s each.")
    
    m3u8_lines = [
        "#EXTM3U",
        "#EXT-X-VERSION:3",
        f"#EXT-X-TARGETDURATION:{int(SEG_DURATION)}",
        "#EXT-X-MEDIA-SEQUENCE:0"
    ]
    
    font_bold = "/usr/share/fonts/liberation-sans/LiberationSans-Bold.ttf"
    font_mono = "/usr/share/fonts/liberation-sans/LiberationSans-Regular.ttf"
    
    for i in range(total_segs):
        start_time = i * SEG_DURATION
        mode_idx = i % 4
        seg_file = f"sintel_smooth_{i:03d}.ts"
        out_path = os.path.join(OUT_DIR, seg_file)
        
        # 1. Configuration by Mode:
        if mode_idx == 0:
            mode_name = "MODE 1: SDR BASELINE (REC.709)"
            color_space_str = "Rec.709 | Gamma 2.4 | 100 nits Target"
            badge_color = "#38bdf8" # Blue
            mode_badge = "[ MODE 1 - SDR (REC.709) ]"
            
            vf_filters = (
                f"scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2,"
                f"drawbox=x=40:y=40:w=1840:h=90:color=black@0.65:t=fill,"
                f"drawbox=x=40:y=40:w=1840:h=90:color={badge_color}:t=2,"
                f"drawtext=fontfile={font_bold}:text='{mode_badge}':fontcolor={badge_color}:fontsize=34:x=60:y=55,"
                f"drawtext=fontfile={font_mono}:text='{color_space_str}':fontcolor=white:fontsize=22:x=60:y=95,"
                f"drawbox=x=1440:y=50:w=420:h=70:color=black@0.8:t=fill,"
                f"drawbox=x=1440:y=50:w=420:h=70:color={badge_color}:t=2,"
                f"drawtext=fontfile={font_bold}:text='SWITCH IN %{{eif\\:ceil(30-t)\\:d}}s':fontcolor=#fbbf24:fontsize=28:x=1460:y=72,"
                f"drawbox=x=40:y=970:w=1840:h=70:color=black@0.65:t=fill,"
                f"drawtext=fontfile={font_mono}:text='SDR Mode (30s Cadence) • 10-bit HEVC • Smooth Transition Test':fontcolor=#cbd5e1:fontsize=22:x=60:y=992"
            )
            x265_opts = (
                "colorprim=bt709:transfer=bt709:colormatrix=bt709:"
                "range=limited:repeat-headers=1:info=1:no-open-gop=1:keyint=60:min-keyint=60"
            )
            
        elif mode_idx == 1:
            mode_name = "MODE 2: HLG BROADCAST HDR (BT.2100)"
            color_space_str = "BT.2100 | ARIB STD-B67 (HLG) | 1000 nits Dynamic"
            badge_color = "#facc15" # Yellow
            mode_badge = "[ MODE 2 - HLG HDR (BT.2100) ]"
            
            vf_filters = (
                f"scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2,"
                f"drawbox=x=40:y=40:w=1840:h=90:color=black@0.65:t=fill,"
                f"drawbox=x=40:y=40:w=1840:h=90:color={badge_color}:t=2,"
                f"drawtext=fontfile={font_bold}:text='{mode_badge}':fontcolor={badge_color}:fontsize=34:x=60:y=55,"
                f"drawtext=fontfile={font_mono}:text='{color_space_str}':fontcolor=white:fontsize=22:x=60:y=95,"
                f"drawbox=x=1440:y=50:w=420:h=70:color=black@0.8:t=fill,"
                f"drawbox=x=1440:y=50:w=420:h=70:color={badge_color}:t=2,"
                f"drawtext=fontfile={font_bold}:text='SWITCH IN %{{eif\\:ceil(30-t)\\:d}}s':fontcolor=#38bdf8:fontsize=28:x=1460:y=72,"
                f"drawbox=x=40:y=970:w=1840:h=70:color=black@0.65:t=fill,"
                f"drawtext=fontfile={font_mono}:text='HLG Broadcast HDR • Hybrid Log-Gamma Tone Curve • ARIB STD-B67':fontcolor=#fef08a:fontsize=22:x=60:y=992"
            )
            x265_opts = (
                "colorprim=bt2020:transfer=arib-std-b67:colormatrix=bt2020nc:"
                "range=limited:repeat-headers=1:info=1:no-open-gop=1:keyint=60:min-keyint=60"
            )
            
        elif mode_idx == 2:
            mode_name = "MODE 3: HDR10 PEAK BRIGHTNESS (PQ BT.2020)"
            color_space_str = "BT.2020 | SMPTE ST 2084 (PQ) | 1000 nits Peak | Master Display\\: D65 P3"
            badge_color = "#f43f5e" # Rose / Red
            mode_badge = "[ MODE 3 - HDR10 (PQ BT.2020) ]"
            
            vf_filters = (
                f"scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2,"
                f"drawbox=x=40:y=40:w=1840:h=90:color=black@0.65:t=fill,"
                f"drawbox=x=40:y=40:w=1840:h=90:color={badge_color}:t=2,"
                f"drawtext=fontfile={font_bold}:text='{mode_badge}':fontcolor={badge_color}:fontsize=34:x=60:y=55,"
                f"drawtext=fontfile={font_mono}:text='{color_space_str}':fontcolor=white:fontsize=22:x=60:y=95,"
                f"drawbox=x=1440:y=50:w=420:h=70:color=black@0.8:t=fill,"
                f"drawbox=x=1440:y=50:w=420:h=70:color={badge_color}:t=2,"
                f"drawtext=fontfile={font_bold}:text='SWITCH IN %{{eif\\:ceil(30-t)\\:d}}s':fontcolor=#facc15:fontsize=28:x=1460:y=72,"
                f"drawbox=x=40:y=970:w=1840:h=70:color=black@0.65:t=fill,"
                f"drawtext=fontfile={font_mono}:text='HDR10 Dynamic Range • SMPTE ST 2084 PQ • Mastering Luminance\\: 1000/0.0001 nits':fontcolor=#fecdd3:fontsize=22:x=60:y=992"
            )
            x265_opts = (
                "colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:"
                "master-display=G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,1):"
                "max-cll=1000,400:range=limited:repeat-headers=1:info=1:no-open-gop=1:keyint=60:min-keyint=60"
            )
            
        else: # mode_idx == 3 (HLG Step-down)
            mode_name = "MODE 4: HLG STEP-DOWN (SMOOTH EASING)"
            color_space_str = "BT.2100 | ARIB STD-B67 (HLG) | Step-Down to prevent decoder shock"
            badge_color = "#eab308" # Amber
            mode_badge = "[ MODE 4 - HLG STEP-DOWN ]"
            
            vf_filters = (
                f"scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2,"
                f"drawbox=x=40:y=40:w=1840:h=90:color=black@0.65:t=fill,"
                f"drawbox=x=40:y=40:w=1840:h=90:color={badge_color}:t=2,"
                f"drawtext=fontfile={font_bold}:text='{mode_badge}':fontcolor={badge_color}:fontsize=34:x=60:y=55,"
                f"drawtext=fontfile={font_mono}:text='{color_space_str}':fontcolor=white:fontsize=22:x=60:y=95,"
                f"drawbox=x=1440:y=50:w=420:h=70:color=black@0.8:t=fill,"
                f"drawbox=x=1440:y=50:w=420:h=70:color={badge_color}:t=2,"
                f"drawtext=fontfile={font_bold}:text='SWITCH IN %{{eif\\:ceil(30-t)\\:d}}s':fontcolor=#38bdf8:fontsize=28:x=1460:y=72,"
                f"drawbox=x=40:y=970:w=1840:h=70:color=black@0.65:t=fill,"
                f"drawtext=fontfile={font_mono}:text='Graceful Step-Down Curve • Preparing hardware surface for SDR baseline':fontcolor=#fef08a:fontsize=22:x=60:y=992"
            )
            x265_opts = (
                "colorprim=bt2020:transfer=arib-std-b67:colormatrix=bt2020nc:"
                "range=limited:repeat-headers=1:info=1:no-open-gop=1:keyint=60:min-keyint=60"
            )
            
        print(f"[{i+1}/{total_segs}] Encoding {seg_file} ({start_time}s - {start_time+SEG_DURATION}s) ➔ {mode_name}...")
        cmd = [
            "ffmpeg", "-y",
            "-ss", str(start_time),
            "-i", SOURCE_VIDEO,
            "-t", str(SEG_DURATION),
            "-vf", vf_filters,
            "-c:v", "libx265", "-preset", "ultrafast", "-crf", "22", "-pix_fmt", "yuv420p10le",
            "-x265-params", x265_opts,
            "-c:a", "aac", "-b:a", "192k", "-ar", "48000",
            "-muxdelay", "0", "-muxpreload", "0",
            out_path
        ]
        subprocess.run(cmd, check=True)
        
        if i > 0:
            m3u8_lines.append("#EXT-X-DISCONTINUITY")
        m3u8_lines.append(f"#EXTINF:{SEG_DURATION:.6f},")
        m3u8_lines.append(seg_file)
        
    m3u8_lines.append("#EXT-X-ENDLIST")
    m3u8_path = os.path.join(OUT_DIR, "hdr_smooth.m3u8")
    with open(m3u8_path, "w") as f:
        f.write("\n".join(m3u8_lines) + "\n")
    print(f"\n✓ Generated master playlist: {m3u8_path} with {total_segs} smooth segments!")

if __name__ == "__main__":
    render_smooth_suite()
