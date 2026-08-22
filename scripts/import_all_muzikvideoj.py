#!/usr/bin/env python3
"""
IPTV Live Bridge - Batch Importer for Curated Esperanto Music Videos & Documentaries
"""

import os
import sys
import subprocess
import re

SRC_DIR = "/home/joop/iptv/downloads/muzikvideoj"
DST_DIR = "/home/joop/iptv/pkg/iptv-live-bridge/esperantotv"
os.makedirs(DST_DIR, exist_ok=True)

# Curated Selection: Best quality music videos and cultural gems
SELECTED_FILES = [
    # Top Music Videos (Martin & la Talpoj, Inicialoj dc, Eterne Rima)
    ("Martin Wiese - Superbazaro", "mv_superbazaro"),
    ("Gefratoj - Martin & la talpoj  (Oficiala muzikvideo)", "mv_gefratoj"),
    ("Berlino sen vi", "mv_berlinosenvi"),
    ("La malpeza dormo - inicialoj dc", "mv_lamalpezadormo"),
    ("Samideano - ETERNE RIMA  (Oficiala muzikvideo)", "mv_samideano"),
    
    # Classic Esperanto Bands (Kajto, Kaj Tiel Plu, LPG, BaRok, Gijom')
    ("Kajto - Tohuvabohuo", "mv_kajto_tohuvabohuo"),
    ("Kajto - La Malnova Balancilo", "mv_kajto_balancilo"),
    ("Kaj Tiel Plu - Mi Volus Esti Dianto", "mv_kajtielplu_dianto"),
    ("La Perdita Generacio - Senpromese, senperfide", "mv_lpg_senpromese"),
    ("Jen Nia Viv-River' - BaRok", "mv_barok_vivriver"),
    ("La postrompiĝa temp’ - Gijom’ Armide", "mv_gijom_postrompiga"),
    ("Oe, oe en la ŝipo - Jacques YVART", "mv_yvart_oeoe"),
    ("Akordo - Printempas", "mv_akordo_printempas"),
    ("La Luna Promenado - Roĝer Borĝes", "mv_roger_luna"),
    ("Ni artas parolante - Daniel Haddad", "mv_haddad_niartas"),
    ("Sufero - Appelez moi personne", "mv_sufero"),
    ("La vivo rozas (Edith Piaf) - Joëlle Rabu", "mv_rabu_lavivorozas"),
    
    # Fun Anime / Pop culture Esperanto covers
    ("Evangelion opening en Esperanto", "mv_evangelion_eo"),
    ("Deathnote opening esperanto", "mv_deathnote_eo"),
    ("One piece op 1 We are en esperanto", "mv_onepiece_eo"),
    ("Pokémon op1 Fandub Esperanto", "mv_pokemon_eo"),
    
    # Documentaries & Speeches
    ("Welcome to the Spirit Sphere ｜ The Esperanto Project ｜ TEDxBangalore", "doc_tedx_bangalore"),
    ("How I became fluent in Esperanto", "doc_fluent_esperanto"),
]

def find_matching_file(prefix):
    for f in os.listdir(SRC_DIR):
        if f.endswith(".part"):
            continue
        if prefix in f:
            return os.path.join(SRC_DIR, f)
    return None

def transcode_video(src_path, tag):
    out_pattern = os.path.join(DST_DIR, f"{tag}_%04d.ts")
    out_m3u8 = os.path.join(DST_DIR, f"{tag}.m3u8")
    
    existing = [f for f in os.listdir(DST_DIR) if f.startswith(f"{tag}_") and f.endswith(".ts")]
    if len(existing) > 0:
        print(f"  ✓ [{tag}] already exists ({len(existing)} segments). Skipping.")
        return
        
    print(f"  ➔ Transcoding '{os.path.basename(src_path)}' into '{tag}'...")
    cmd = [
        "ffmpeg", "-y",
        "-i", src_path,
        "-vf", "scale=1280:720:force_original_aspect_ratio=decrease,pad=1280:720:(ow-iw)/2:(oh-ih)/2",
        "-c:v", "libx264", "-preset", "ultrafast", "-crf", "23",
        "-c:a", "aac", "-b:a", "192k", "-ar", "48000",
        "-f", "segment", "-segment_time", "6", "-segment_list", out_m3u8,
        out_pattern
    ]
    res = subprocess.run(cmd, capture_output=True, text=True)
    if res.returncode == 0:
        segs = [f for f in os.listdir(DST_DIR) if f.startswith(f"{tag}_") and f.endswith(".ts")]
        print(f"  ✓ Successfully encoded {len(segs)} segments for {tag}!")
    else:
        print(f"  ✗ Error encoding {tag}: {res.stderr[-200:]}")

def run():
    print(f"=== Batch Importing Curated Esperanto Library ===")
    for title_prefix, tag in SELECTED_FILES:
        filepath = find_matching_file(title_prefix)
        if filepath:
            transcode_video(filepath, tag)
        else:
            print(f"  ⚠️ File not found matching '{title_prefix}'")

if __name__ == "__main__":
    run()
