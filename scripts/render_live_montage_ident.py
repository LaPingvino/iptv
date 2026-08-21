#!/usr/bin/env python3
"""
Esperanto TV - Authentic High-Energy Community & Event Montage Ident
Features real human energy, youth excitement, and festival moments from:
  1. Esperanto Senlime (Challenge energy & running / travel)
  2. KEF 2005: La Plejpleja Festivalo (Crowd concert excitement)
  3. Universala Kongreso (Global cultural celebration)
  4. Superbazaro (Rock energy)
  5. Clean Minimalist Branding Slate: ESPERANTO on top, Star in center, BIG TV at bottom!
"""

import os
import subprocess
import glob

OUT_DIR = "/home/joop/iptv/pkg/iptv-live-bridge/testcard"
os.makedirs(OUT_DIR, exist_ok=True)
OUT_TS = os.path.join(OUT_DIR, "esperanto_ident0.ts")

# 1. Find dynamic sources from live events and Senlime
kef_files = glob.glob("/home/joop/iptv/downloads/*Plejpleja*.*")
kef_src = kef_files[0] if kef_files else None

uk_src = "/home/joop/iptv/downloads/muzikvideoj/2017 universala kongreso de esperanto en seulo [ZdXiFcyAs2o].mkv"
superbazaro_src = "/home/joop/iptv/downloads/muzikvideoj/Martin Wiese - Superbazaro [gWiH8BlpU0U].mkv"

senlime_files = [f for f in glob.glob("/home/joop/iptv/downloads/esperantosenlime/*.*") if not f.endswith(".part") and not f.endswith(".tmp") and not "anonco" in f.lower()]
senlime_src = senlime_files[0] if senlime_files else None

CLIPS = [
    (senlime_src, 95.0, 1.4, "Esperanto Senlime (Youth energy & travel)"),
    (kef_src, 140.0, 1.4, "KEF 2005 Festival (Concert crowd & celebration)"),
    (uk_src, 320.0, 1.4, "Universala Kongreso (Global community)"),
    (superbazaro_src, 35.0, 1.4, "Superbazaro (Rock concert energy)"),
    (senlime_src, 210.0, 1.4, "Esperanto Senlime (Team laughter & action)"),
]

def render():
    print("=== Rendering Authentic Event & Senlime Montage Ident ===")
    
    cut_files = []
    for idx, (path, start, dur, label) in enumerate(CLIPS):
        if not path or not os.path.exists(path):
            print(f"Skipping {label} (not found)")
            continue
        out_part = f"/tmp/event_part_{idx}.mp4"
        cmd = [
            "ffmpeg", "-y",
            "-ss", str(start),
            "-i", path,
            "-t", str(dur),
            "-vf", "scale=1920:1080:force_original_aspect_ratio=increase,crop=1920:1080,setsar=1,fps=60",
            "-c:v", "libx264", "-preset", "ultrafast", "-crf", "18", "-pix_fmt", "yuv420p",
            "-an",
            out_part
        ]
        subprocess.run(cmd, check=True)
        cut_files.append(out_part)
        print(f"  ✓ Prepared [{idx+1}] {label} ({dur}s)")

    inputs = []
    for f in cut_files:
        inputs.extend(["-i", f])
        
    num_cuts = len(cut_files)
    font_bold = "/usr/share/fonts/liberation-sans/LiberationSans-Bold.ttf"
    
    # Build filtergraph
    cut_pads = "".join([f"[{i}:v]" for i in range(num_cuts)])
    
    filter_complex = (
        f"{cut_pads}concat=n={num_cuts}:v=1:a=0[montage];"
        
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
        
        # Upbeat melodic fanfare & celebratory major chords + deep bass drop
        "sine=frequency=440:duration=0.3,afade=t=out:st=0.2:d=0.1,adelay=0|0[j1];"
        "sine=frequency=554.37:duration=0.3,afade=t=out:st=0.2:d=0.1,adelay=300|300[j2];"
        "sine=frequency=659.25:duration=0.3,afade=t=out:st=0.2:d=0.1,adelay=600|600[j3];"
        "sine=frequency=880.00:duration=0.6,afade=t=out:st=0.4:d=0.2,adelay=900|900[j4];"
        "sine=frequency=523.25:duration=0.3,afade=t=out:st=0.2:d=0.1,adelay=1500|1500[j5];"
        "sine=frequency=659.25:duration=0.3,afade=t=out:st=0.2:d=0.1,adelay=1800|1800[j6];"
        "sine=frequency=783.99:duration=0.3,afade=t=out:st=0.2:d=0.1,adelay=2100|2100[j7];"
        "sine=frequency=1046.50:duration=0.6,afade=t=out:st=0.4:d=0.2,adelay=2400|2400[j8];"
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
    print(f"✓ Rendered Authentic Event Montage Ident: {OUT_TS} ({os.path.getsize(OUT_TS)} bytes)")

if __name__ == "__main__":
    render()
