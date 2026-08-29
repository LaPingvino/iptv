#!/usr/bin/env python3
"""
IPTV EPG Generator - Deterministic XMLTV Program Guide for 24/7 Linear Channels
Generates XMLTV <programme> blocks synchronized with the wall-clock epoch modulo broadcast scheduler.
"""

import os
import time
import datetime
from collections import defaultdict
from itertools import groupby

# Metadata Dictionary for Esperanto TV
ESPERANTO_METADATA = {
    "mazi": {
        "title": "Mazi en Gondolando",
        "desc": "La legenda animacia kurso de Esperanto. La reĝo de Gondolando, Silvia, Bob kaj la eksterterano Mazi en amuza kaj eduka aventuro.",
        "category": "Animacio / Kurso"
    },
    "dok_kef2005": {
        "title": "KEF 2005: La Plejpleja Festivalo",
        "desc": "Dokumenta filmo pri la kultura Esperanto-festivalo en Helsinki kun koncertoj, arto kaj festivala etoso.",
        "category": "Dokumentario"
    },
    "mv_superbazaro": {
        "title": "Muzikvideo: Superbazaro (Martin & la Talpoj)",
        "desc": "Oficiala muzikvideo de la rok-kanto 'Superbazaro' de Martin & la Talpoj.",
        "category": "Muziko"
    },
    "mv_gefratoj": {
        "title": "Muzikvideo: Gefratoj (Inicialoj dc)",
        "desc": "Elektropopa muzikvideo de Inicialoj dc.",
        "category": "Muziko"
    },
    "mv_berlinosenvi": {
        "title": "Muzikvideo: Berlino sen vi (Inicialoj dc)",
        "desc": "Elektronika muzikvideo pri Berlino de Inicialoj dc.",
        "category": "Muziko"
    },
}

# Add Pasporto episodes (1 to 16)
PASPORTO_EP_INFO = {
    1: ("Bonvenon al nia hejmo!", "Enkonduko al la familio Bonvolo kaj ilia gastiga hejmo."),
    2: ("Kiu estas tiu?", "Novaj gastoj alvenas kaj misteraj situacioj komenciĝas."),
    3: ("La perdita valizo", "Serĉado de perdita valizo kaj amuzaj miskomprenoj."),
    4: ("Surprizo en la kuirejo", "Kuirartaj aventuroj kaj nekutimaj petoj."),
    5: ("La granda festo", "Preparado por granda familia festo kun amikoj."),
    6: ("Nekonata vizitanto", "Mistera vizitanto aperas ĉe la pordo."),
    7: ("La sekreto malkaŝita", "Gravaj sekretoj kaj komediaj klarigoj."),
    8: ("Vojaĝaj planoj", "La familio kaj gastoj planas novajn vojaĝojn tra la mondo."),
    9: ("Trajnoj kaj biletoj", "Aventuroj ĉe la stacidomo."),
    10: ("Aventuro en la urbo", "Esplorado de nova urbo kaj renkontoj."),
    11: ("La hotelo", "Restado en hotelo kun neatenditaj surprizoj."),
    12: ("La restoracio", "Mendo de manĝaĵoj kaj lingvaj defioj."),
    13: ("Sur la strando", "Someraj ferioj kaj amuzaj agadoj ĉe la maro."),
    14: ("La muzeo", "Kultura vizito al muzeo kun historiaj sekretoj."),
    15: ("La adiaŭa vespero", "Gaja vespero antaŭ la reveno hejmen."),
    16: ("Ĝis revido, amikoj!", "La granda finalo de Pasporto al la Tuta Mondo."),
}

for ep, (subtitle, desc) in PASPORTO_EP_INFO.items():
    ESPERANTO_METADATA[f"pasporto_{ep:02d}"] = {
        "title": f"Pasporto al la Tuta Mondo - Ep. {ep}: {subtitle}",
        "desc": f"{desc} Komedia instrua serio por lernantoj de Esperanto.",
        "category": "Serio / Edukado"
    }

# Add Esperanto Senlime episodes (1 to 16)
SENLIME_EP_INFO = {
    1: ("La unua serio en Esperanto", "La teamoj ekas sian grandan vojaĝon tra Eŭropo."),
    2: ("Konstruado per dolĉaĵoj kaj spagetoj", "Krea defio uzanta nur dolĉaĵojn kaj spagetojn."),
    3: ("Riskante la vivon sur du radoj", "Biciklaj defioj kaj rapidaj kuroj tra la urbo."),
    4: ("Filmproduktado en trajnoj", "Kreado de filmetoj dum veturado per trajno."),
    5: ("La venĝo de la trajnoj", "Fervojaj misaventuroj kaj horar-defioj."),
    6: ("Rilaksado inter bestoj", "Vizito al bestoj kaj trankvilaj momentoj."),
    7: ("ASMR kun arboj", "Nekutima kaj amuza natura ASMR-defio."),
    8: ("Pluvo kaj suno", "Veteraj defioj dum la subĉiela vojaĝo."),
    9: ("Vojaĝi sen biletoj", "Strategiaj vojaĝdefioj kaj amuzaj taskoj."),
    10: ("Mangirdito scias kion vi faris", "Misteraj ludoj kaj teamaj taktikoj."),
    11: ("Supren, suben kaj akven", "Akvo-defioj kaj sportaj agadoj."),
    12: ("Plaĝa tago", "Amuzaj ludoj kaj defioj ĉe la marbordo."),
    13: ("La voko de Ĥthuluzo", "Mistera vespera defio kun mitologia etoso."),
    14: ("Nia plej granda perdo", "Dramaj momentoj kaj poentaj ŝanĝoj."),
    15: ("Gajni plej gravas", "La granda antaŭ-finala konkurso."),
    16: ("Ni nur parolas Esperanton", "La grandioza finalo de Esperanto Senlime Sezono 1!"),
}

for ep, (subtitle, desc) in SENLIME_EP_INFO.items():
    ESPERANTO_METADATA[f"senlime_s01e{ep:02d}"] = {
        "title": f"Esperanto Senlime - S1Ĉ{ep:02d}: {subtitle}",
        "desc": f"{desc} Realspektaklo de junaj esperantistoj vojaĝantaj tra Eŭropo.",
        "category": "Realspektaklo / Junularo"
    }

for part in range(1, 7):
    ESPERANTO_METADATA[f"dok_estas_parto_{part}"] = {
        "title": f"Esperanto estas lingvo... (Parto {part})",
        "desc": f"Dokumenta serio pri la historio, kulturo kaj komunumo de la lingvo Esperanto (Parto {part}).",
        "category": "Dokumentario"
    }

def format_xmltv_time(epoch_sec):
    """Formats epoch seconds into XMLTV timestamp (YYYYMMDDhhmmss +0000)."""
    dt = datetime.datetime.fromtimestamp(epoch_sec, datetime.timezone.utc)
    return dt.strftime("%Y%m%d%H%M%S +0000")

def get_channel_schedule_blocks(media_dir, default_meta=None, seg_duration=10.0):
    """Calculates ordered schedule blocks from directory TS files matching Go LinearStation."""
    if not os.path.exists(media_dir):
        return []
    ts_files = sorted([f for f in os.listdir(media_dir) if f.endswith(".ts")])
    if not ts_files:
        return []

    # If Esperanto TV, perform the exact same round-by-round episodic interleaving as Go linear.go!
    if "esperanto" in media_dir.lower():
        shows = defaultdict(list)
        bumper = []
        for s in ts_files:
            if s.startswith("dok_estas_parto_01_"):
                bumper.append(s)
            elif s.startswith("ident_z_"):
                continue
            else:
                k = s.rsplit("_", 1)[0]
                shows[k].append(s)

        pasporto = [shows[f"pasporto_{i:02d}"] for i in range(1, 17) if f"pasporto_{i:02d}" in shows]
        senlime = [shows[f"senlime_s01e{i:02d}"] for i in range(1, 17) if f"senlime_s01e{i:02d}" in shows]

        mv_keys = sorted([k for k in shows if k.startswith("mv_")])
        mv_blocks = []
        for i in range(0, len(mv_keys), 2):
            b = []
            for k in mv_keys[i:i+2]:
                b.extend(shows[k])
            if b:
                mv_blocks.append(b)

        doc_keys = sorted([k for k in shows if k.startswith("dok_")])
        specials = [shows[k] for k in doc_keys]
        if "mazi" in shows:
            mazi = shows["mazi"]
            chunk_size = 115
            for i in range(0, len(mazi), chunk_size):
                specials.append(mazi[i:i+chunk_size])

        max_rounds = max(len(pasporto), len(senlime), len(specials), len(mv_blocks))
        blocks = []
        bumper_block = {
            "title": "Esperanto Estas: Enkonduko",
            "desc": "Oficiala stacia vineto kaj enkonduko al la internacia lingvo Esperanto.",
            "category": "Vineto",
            "duration": len(bumper) * seg_duration
        }

        for r in range(max_rounds):
            # 1. Pasporto
            if r < len(pasporto):
                ep = r + 1
                info = PASPORTO_EP_INFO.get(ep, (f"Epizodo {ep}", ""))
                blocks.append({
                    "title": f"Pasporto al la Tuta Mondo - Epizodo {ep}: {info[0]}",
                    "desc": info[1] or f"Epizodo {ep} de la internacia realspektaklo Pasporto al la Tuta Mondo.",
                    "category": "Kurso / Komedio",
                    "duration": len(pasporto[r]) * seg_duration
                })
                if bumper:
                    blocks.append(bumper_block)

            # 2. Senlime
            if r < len(senlime):
                ep = r + 1
                info = SENLIME_EP_INFO.get(ep, (f"Epizodo {ep}", ""))
                blocks.append({
                    "title": f"Esperanto Senlime - Epizodo {ep}: {info[0]}",
                    "desc": info[1] or f"Moderna vojaĝa kaj lingva realspektaklo tra Eŭropo (Epizodo {ep}).",
                    "category": "Realspektaklo / Junularo",
                    "duration": len(senlime[r]) * seg_duration
                })
                if bumper:
                    blocks.append(bumper_block)

            # 3. Music Video Block
            if r < len(mv_blocks):
                blocks.append({
                    "title": "Esperanto-Muziko (Muzikvideoj)",
                    "desc": "Kolekto de popularaj Esperanto-muzikvideoj kaj kantoj.",
                    "category": "Muziko",
                    "duration": len(mv_blocks[r]) * seg_duration
                })
                if bumper:
                    blocks.append(bumper_block)

            # 4. Special (Docs / Mazi)
            if r < len(specials):
                s_segs = specials[r]
                title = "Dokumenta Filmo"
                desc = "Esperanto-dokumentario aŭ animacia klasikaĵo."
                cat = "Dokumentario"
                first = s_segs[0]
                if "mazi" in first:
                    title = "Mazi en Gondolando"
                    desc = "Klasika animacia Esperanto-kurso kun Mazi, Silvia kaj Bob."
                    cat = "Animacio"
                elif "kef2005" in first:
                    title = "KEF 2005: La Plejpleja Festivalo"
                    desc = "Kultura Esperanto-Festivalo en Helsinki."
                elif "dok_estas" in first:
                    title = "Esperanto Estas: Dokumentario"
                    desc = "Dokumenta serio pri la historio kaj moderna komunumo de Esperanto."
                blocks.append({
                    "title": title,
                    "desc": desc,
                    "category": cat,
                    "duration": len(s_segs) * seg_duration
                })
                if bumper:
                    blocks.append(bumper_block)

        return blocks

    # Default fallback for other directories (Bahá'í TV, etc.)
    blocks = []
    for prefix, group in groupby(ts_files, key=lambda f: f.rsplit("_", 1)[0]):
        count = len(list(group))
        dur_sec = count * seg_duration
        
        meta = {}
        if default_meta and prefix in default_meta:
            meta = default_meta[prefix]
        else:
            clean_title = prefix.replace("_", " ").title()
            meta = {
                "title": clean_title,
                "desc": f"Broadcast of {clean_title}",
                "category": "General"
            }
            
        blocks.append({
            "prefix": prefix,
            "title": meta.get("title", prefix),
            "desc": meta.get("desc", ""),
            "category": meta.get("category", "General"),
            "duration": dur_sec
        })
    return blocks

def generate_xmltv_programmes(channel_id, channel_name, blocks, days_back=1, days_ahead=7):
    """Generates XMLTV <programme> XML string spanning days_back to days_ahead."""
    if not blocks:
        return ""
        
    total_loop_dur = sum(b["duration"] for b in blocks)
    if total_loop_dur <= 0:
        return ""
        
    now = time.time()
    start_window = now - (days_back * 86400)
    end_window = now + (days_ahead * 86400)
    
    # Calculate initial offset at start_window
    offset = start_window % total_loop_dur
    cur_pos = 0
    cur_prog_idx = 0
    for i, b in enumerate(blocks):
        if cur_pos + b["duration"] > offset:
            cur_prog_idx = i
            prog_start = start_window - (offset - cur_pos)
            break
        cur_pos += b["duration"]
        
    xml_lines = []
    curr_time = prog_start
    curr_idx = cur_prog_idx
    
    while curr_time < end_window:
        prog = blocks[curr_idx]
        p_start = curr_time
        p_stop = curr_time + prog["duration"]
        
        start_str = format_xmltv_time(p_start)
        stop_str = format_xmltv_time(p_stop)
        
        title_escaped = prog["title"].replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")
        desc_escaped = prog["desc"].replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")
        cat_escaped = prog["category"].replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")
        
        xml_lines.append(f'  <programme start="{start_str}" stop="{stop_str}" channel="{channel_id}">')
        xml_lines.append(f'    <title lang="eo">{title_escaped}</title>')
        if desc_escaped:
            xml_lines.append(f'    <desc lang="eo">{desc_escaped}</desc>')
        if cat_escaped:
            xml_lines.append(f'    <category lang="eo">{cat_escaped}</category>')
        xml_lines.append('  </programme>')
        
        curr_time = p_stop
        curr_idx = (curr_idx + 1) % len(blocks)
        
    return "\n".join(xml_lines)

def generate_standalone_epg_xml(channel_id, channel_name, media_dir, metadata_dict, seg_duration=10.0):
    """Generates complete standalone XMLTV document."""
    blocks = get_channel_schedule_blocks(media_dir, metadata_dict, seg_duration=seg_duration)
    programmes_xml = generate_xmltv_programmes(channel_id, channel_name, blocks)
    
    return f"""<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE tv SYSTEM "xmltv.dtd">
<tv source-info-url="https://kiefte.eu/iptv" generator-info-name="IPTV Live Bridge Deterministic EPG Engine">
  <channel id="{channel_id}">
    <display-name>{channel_name}</display-name>
  </channel>
{programmes_xml}
</tv>
"""

if __name__ == "__main__":
    esp_dir = "/home/joop/iptv/pkg/iptv-live-bridge/esperantotv"
    xml_out = generate_standalone_epg_xml("EsperantoTV.eo@SD", "Esperanto TV", esp_dir, ESPERANTO_METADATA)
    print(f"Generated XMLTV EPG ({len(xml_out)} bytes)")
    lines = xml_out.strip().split("\n")
    print(f"Total Lines: {len(lines)}")
    print("\n--- Preview first 20 lines ---")
    print("\n".join(lines[:20]))


def generate_twitch_epg_programmes(channel_id, channel_name, category_name="Gaming", days_back=1, days_ahead=7):
    """Generates synthetic 2h-4h program blocks for Twitch channels with fallback/live info."""
    now = time.time()
    start_window = now - (days_back * 86400)
    end_window = now + (days_ahead * 86400)
    
    # Align to clean hours
    block_dur = 3 * 3600 # 3 hours per block
    curr_time = start_window - (start_window % block_dur)
    
    templates = [
        ("Live Stream & Community Broadcasts", f"24/7 live stream featuring {channel_name}, community challenges, and world-record attempts."),
        ("Top Speedruns & Highlights", f"High-level gameplay and marathon runs with featured speedrun showcases."),
        ("Speedrun Practice & Leaderboard Races", f"Continuous live gaming rotation across top active community runners and tournament broadcasts."),
        ("World Record Attempts & Practice", f"Live speedruns, practice routing, and challenge marathons with daily speedrun rotation.")
    ]
    
    xml_lines = []
    idx = int((curr_time // block_dur) % len(templates))
    
    while curr_time < end_window:
        p_start = curr_time
        p_stop = curr_time + block_dur
        
        start_str = format_xmltv_time(p_start)
        stop_str = format_xmltv_time(p_stop)
        
        title, desc = templates[idx]
        title_full = f"{channel_name}: {title}".replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")
        desc_escaped = desc.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")
        cat_escaped = category_name.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")
        
        xml_lines.append(f'  <programme start="{start_str}" stop="{stop_str}" channel="{channel_id}">')
        xml_lines.append(f'    <title lang="en">{title_full}</title>')
        xml_lines.append(f'    <desc lang="en">{desc_escaped}</desc>')
        xml_lines.append(f'    <category lang="en">{cat_escaped}</category>')
        xml_lines.append('  </programme>')
        
        curr_time = p_stop
        idx = (idx + 1) % len(templates)
        
    return "\n".join(xml_lines)
