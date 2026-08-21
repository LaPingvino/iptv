#!/usr/bin/env python3
"""
IPTV Live Bridge - Master Esperanto TV Library Builder
Transcodes and segments:
1. Pasporto al la Tuta Mondo (Full 16 episodes)
2. Esperanto estas lingvo... (Parts 01 - 06)
3. La Plejpleja Festivalo (KEF 2005 Documentary)
4. Curated Music Videos (Martin & la Talpoj, Inicialoj dc, Kajto, Kaj Tiel Plu, LPG, etc.)
"""

import os
import sys
import subprocess
import glob
import re

DOWNLOADS_DIR = "/home/joop/iptv/downloads"
MV_DIR = os.path.join(DOWNLOADS_DIR, "muzikvideoj")
DST_DIR = "/home/joop/iptv/pkg/iptv-live-bridge/esperantotv"
os.makedirs(DST_DIR, exist_ok=True)

def transcode_file(src_path, tag):
    out_pattern = os.path.join(DST_DIR, f"{tag}_%04d.ts")
    out_m3u8 = os.path.join(DST_DIR, f"{tag}.m3u8")
    
    existing = [f for f in os.listdir(DST_DIR) if f.startswith(f"{tag}_") and f.endswith(".ts")]
    if len(existing) > 5:
        print(f"  ✓ [{tag}] already segmented ({len(existing)} segments). Skipping.")
        return
        
    print(f"  ➔ Transcoding '{os.path.basename(src_path)}' -> '{tag}'...")
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
        print(f"  ✓ Successfully encoded {len(segs)} segments for '{tag}'!")
    else:
        print(f"  ✗ Error encoding '{tag}': {res.stderr[-150:]}")

def process_pasporto():
    print("\n=== 1. Processing 'Pasporto al la Tuta Mondo' (16 Episodes) ===")
    files = glob.glob(os.path.join(DOWNLOADS_DIR, "Pasporto*.*"))
    
    # Sort files by episode number
    def ep_num(f):
        m = re.search(r'Pasporto al la [Tt]uta [Mm]ondo\s*(\d+)', os.path.basename(f))
        return int(m.group(1)) if m else 99
        
    sorted_files = sorted(files, key=ep_num)
    for f in sorted_files:
        if f.endswith(".part"): continue
        num = ep_num(f)
        tag = f"pasporto_{num:02d}"
        transcode_file(f, tag)

def process_esperanto_estas():
    print("\n=== 2. Processing 'Esperanto estas lingvo...' Series ===")
    files = sorted(glob.glob(os.path.join(DOWNLOADS_DIR, "Parto*.*")))
    for f in files:
        if f.endswith(".part"): continue
        m = re.search(r'Parto\s*([0-9A-Za-z]+)', os.path.basename(f))
        part_tag = m.group(1).lower() if m else "misc"
        tag = f"dok_estas_parto_{part_tag}"
        transcode_file(f, tag)

def process_documentaries():
    print("\n=== 3. Processing Feature Documentaries ===")
    kef = glob.glob(os.path.join(DOWNLOADS_DIR, "*Plejpleja*.*"))
    for f in kef:
        if f.endswith(".part"): continue
        transcode_file(f, "dok_kef2005")

def process_muzikvideoj():
    print("\n=== 4. Processing Curated Music Videos ===")
    if not os.path.exists(MV_DIR):
        return
        
    # Mapping of filename keyword to tag
    curated_map = [
        ("Superbazaro", "mv_superbazaro"),
        ("Gefratoj", "mv_gefratoj"),
        ("Berlino sen vi", "mv_berlinosenvi"),
        ("La fina venk", "mv_lafinavenk"),
        ("La malpeza dormo", "mv_lamalpezadormo"),
        ("Samideano", "mv_samideano"),
        ("Tohuvabohuo", "mv_kajto_tohuvabohuo"),
        ("La Malnova Balancilo", "mv_kajto_balancilo"),
        ("Mi Volus Esti Dianto", "mv_kajtielplu_dianto"),
        ("Senpromese, senperfide", "mv_lpg_senpromese"),
        ("Jen Nia Viv-River", "mv_barok_vivriver"),
        ("La postrompiĝa temp", "mv_gijom_postrompiga"),
        ("Oe, oe en la ŝipo", "mv_yvart_oeoe"),
        ("Printempas", "mv_akordo_printempas"),
        ("La Luna Promenado", "mv_roger_luna"),
        ("Ni artas parolante", "mv_haddad_niartas"),
        ("Sufero", "mv_sufero"),
        ("La vivo rozas", "mv_rabu_lavivorozas"),
        ("Malbona Pomo", "mv_touhou_badapple_eo"),
        ("Patema", "mv_patema_eo"),
        ("Evangelion", "mv_evangelion_eo"),
        ("Deathnote", "mv_deathnote_eo"),
        ("One piece", "mv_onepiece_eo"),
        ("Pokémon", "mv_pokemon_eo"),
        ("LA KOLOMBINO", "mv_kolombino"),
        ("LA SONO DE SILENTO", "mv_sono_de_silento"),
        ("Yesterday", "mv_yesterday_eo"),
        ("DANKAS MI LA VIVON", "mv_dankas_vivon"),
        ("Anjo Amika", "mv_anjo_avemaria"),
    ]
    
    mv_files = os.listdir(MV_DIR)
    for kw, tag in curated_map:
        matched = None
        for f in mv_files:
            if f.endswith(".part"): continue
            if kw.lower() in f.lower():
                matched = os.path.join(MV_DIR, f)
                break
        if matched:
            transcode_file(matched, tag)
        else:
            print(f"  ⚠️ No file found for keyword '{kw}'")

def process_senlime():
    print("\n=== 5. Processing 'Esperanto Senlime' Reality Show Episodes ===")
    senlime_dir = os.path.join(DOWNLOADS_DIR, "esperantosenlime")
    if not os.path.exists(senlime_dir):
        return
        
    for f in sorted(os.listdir(senlime_dir)):
        if f.endswith(".part") or f.endswith(".tmp") or f.startswith("."):
            continue
        # Strictly require season + episode pattern (e.g. S1Ĉ07 or S01E07), skipping non-episodes
        m = re.search(r'S(\d+)Ĉ?(\d+)', f, re.IGNORECASE)
        if m:
            season = int(m.group(1))
            ep = int(m.group(2))
            tag = f"senlime_s{season:02d}e{ep:02d}"
            full_path = os.path.join(senlime_dir, f)
            transcode_file(full_path, tag)
        else:
            print(f"  ℹ️ Skipping non-episode asset: '{f}'")

def main():
    print(f"==================================================")
    print(f"  ESPERANTO TV MASTER BROADCAST INGESTION PIPELINE")
    print(f"==================================================")
    process_pasporto()
    process_esperanto_estas()
    process_documentaries()
    process_muzikvideoj()
    process_senlime()
    print("\n✓ Ingestion complete!")

if __name__ == "__main__":
    main()
