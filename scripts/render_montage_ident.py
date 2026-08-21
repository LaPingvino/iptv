#!/usr/bin/env python3
"""
Esperanto TV - High-Energy Dynamic Montage Station Ident
Creates a 10-second fast-paced broadcast bumper featuring rapid cuts from:
  1. Mazi en Gondolando (Iconic animation)
  2. Martin Wiese - Superbazaro (Rock energy)
  3. Berlino sen vi - Inicialoj dc (Synthpop energy)
  4. Samideano - Eterne Rima (Hip-hop pulse)
  5. BaRok - Jen Nia Viv-River' (Metal power)
  6. Final Branding Slate with Verda Stelo & Musical TV Fanfare!
"""

import os
import subprocess

OUT_DIR = "/home/joop/iptv/pkg/iptv-live-bridge/testcard"
os.makedirs(OUT_DIR, exist_ok=True)
OUT_TS = os.path.join(OUT_DIR, "esperanto_ident0.ts")

# Source clips with timestamps (start_sec, duration_sec)
CLIPS = [
    ("/home/joop/Mazi en Gondolando.avi", 120.0, 1.4), # Mazi clock eating / fun action
    ("/home/joop/iptv/downloads/muzikvideoj/Martin Wiese - Superbazaro [gWiH8BlpU0U].mkv", 35.0, 1.4), # Superbazaro guitar
    ("/home/joop/iptv/downloads/muzikvideoj/Berlino sen vi [uRQeV9zRgKY].mkv", 42.0, 1.4), # Berlino synth beat
    ("/home/joop/iptv/downloads/muzikvideoj/Samideano - ETERNE RIMA  (Oficiala muzikvideo) [PrHU_lICydA].mkv", 28.0, 1.4), # Rap action
    ("/home/joop/iptv/downloads/muzikvideoj/Jen Nia Viv-River' - BaRok (Esperanto music) [z_K0OpncQc8].mkv", 55.0, 1.4), # Metal guitar
]

def render_montage():
    print("=== Rendering High-Energy Esperanto TV Montage Ident ===")
    
    # 1. Prepare 5 fast cut video segments in /tmp/
    cut_files = []
    for idx, (path, start, dur) in enumerate(CLIPS):
        if not os.path.exists(path):
            print(f"Warning: {path} not found, checking alternatives...")
            continue
        out_part = f"/tmp/ident_part_{idx}.mp4"
        cmd = [
            "ffmpeg", "-y",
            "-ss", str(start),
            "-i", path,
            "-t", str(dur),
            "-vf", "scale=1920:1080:force_original_aspect_ratio=increase,crop=1920:1080,fps=60",
            "-c:v", "libx264", "-preset", "ultrafast", "-crf", "18", "-pix_fmt", "yuv420p",
            "-an",
            out_part
        ]
        subprocess.run(cmd, check=True)
        cut_files.append(out_part)
        print(f"  ✓ Prepared cut #{idx+1} ({dur}s)")

    # 2. Concat the 5 fast cuts + Final Animated Station Branding Title Slate (3s)
    # Total runtime: 5 * 1.4s + 3.0s = 10.0 seconds!
    
    # Let's build the final high-energy composite ffmpeg pipeline
    inputs = []
    for f in cut_files:
        inputs.extend(["-i", f])
        
    font_bold = "/usr/share/fonts/liberation-sans/LiberationSans-Bold.ttf"
    font_regular = "/usr/share/fonts/liberation-sans/LiberationSans-Regular.ttf"
    
    # Dynamic filter graph:
    # Concatenate the 5 video cuts, then transition into the radiant Verda Stelo broadcast branding
    filter_complex = (
        # 5 fast video cuts
        "[0:v][1:v][2:v][3:v][4:v]concat=n=5:v=1:a=0[montage];"
        
        # 3.0s Clean Broadcast Logo Slate: ESPERANTO on top, Star in center, BIG TV at the bottom!
        "color=c=#022c22:s=1920x1080:r=60:d=3[title_bg];"
        "[title_bg]drawbox=x=80:y=80:w=1760:h=920:color=#10b981:t=6[tb1];"
        "[tb1]drawbox=x=100:y=100:w=1720:h=880:color=#064e3b@0.85:t=fill[tb2];"
        "[tb2]drawbox=x=120:y=120:w=1680:h=840:color=#fbbf24@0.6:t=2[tb3];"
        "[tb3]drawtext=fontfile=" + font_bold + ":text='★':fontcolor=#fbbf24:fontsize=160:x=(w-text_w)/2:y=200[ts1];"
        "[ts1]drawtext=fontfile=" + font_bold + ":text='E S P E R A N T O':fontcolor=#34d399:fontsize=64:x=(w-text_w)/2:y=400[tt1];"
        "[tt1]drawbox=x=(iw-360)/2:y=485:w=360:h=4:color=#fbbf24:t=fill[td1];"
        "[td1]drawtext=fontfile=" + font_bold + ":text='TV':fontcolor=white:fontsize=150:x=(w-text_w)/2:y=520[title_out];"
        
        # Concat montage + title
        "[montage][title_out]concat=n=2:v=1:a=0[vout];"
        
        # High Energy TV Jingle: Upbeat melodic brass & synth arpeggio fanfare + final resonant chord
        "sine=frequency=440:duration=0.3,afade=t=out:st=0.2:d=0.1,adelay=0|0[j1];"
        "sine=frequency=554.37:duration=0.3,afade=t=out:st=0.2:d=0.1,adelay=300|300[j2];"
        "sine=frequency=659.25:duration=0.3,afade=t=out:st=0.2:d=0.1,adelay=600|600[j3];"
        "sine=frequency=880.00:duration=0.6,afade=t=out:st=0.4:d=0.2,adelay=900|900[j4];"
        "sine=frequency=523.25:duration=0.3,afade=t=out:st=0.2:d=0.1,adelay=1500|1500[j5];"
        "sine=frequency=659.25:duration=0.3,afade=t=out:st=0.2:d=0.1,adelay=1800|1800[j6];"
        "sine=frequency=783.99:duration=0.3,afade=t=out:st=0.2:d=0.1,adelay=2100|2100[j7];"
        "sine=frequency=1046.50:duration=0.6,afade=t=out:st=0.4:d=0.2,adelay=2400|2400[j8];"
        # Grand Finale Chord on Title Card: C major (523Hz + 659Hz + 784Hz + 1046Hz) with deep 130Hz bass drop!
        "sine=frequency=130.81:duration=3.0,afade=t=out:st=2.2:d=0.8,adelay=7000|7000[fc0];"
        "sine=frequency=523.25:duration=3.0,afade=t=out:st=2.2:d=0.8,adelay=7000|7000[fc1];"
        "sine=frequency=659.25:duration=3.0,afade=t=out:st=2.2:d=0.8,adelay=7000|7000[fc2];"
        "sine=frequency=783.99:duration=3.0,afade=t=out:st=2.2:d=0.8,adelay=7000|7000[fc3];"
        "sine=frequency=1046.50:duration=3.0,afade=t=out:st=2.2:d=0.8,adelay=7000|7000[fc4];"
        "[j1][j2][j3][j4][j5][j6][j7][j8][fc0][fc1][fc2][fc3][fc4]amix=inputs=13:normalize=0,volume=0.5,aformat=channel_layouts=stereo:sample_rates=48000[aout]"
    )
    
    cmd = ["ffmpeg", "-y"] + inputs + [
        "-filter_complex", filter_complex,
        "-map", "[vout]", "-map", "[aout]",
        "-c:v", "libx264", "-preset", "ultrafast", "-crf", "18", "-pix_fmt", "yuv420p",
        "-c:a", "aac", "-b:a", "192k", "-ar", "48000",
        OUT_TS
    ]
    
    print("Rendering final master ident...")
    subprocess.run(cmd, check=True)
    print(f"✓ Successfully rendered High-Energy Montage Ident: {OUT_TS} ({os.path.getsize(OUT_TS)} bytes)")

if __name__ == "__main__":
    render_montage()
