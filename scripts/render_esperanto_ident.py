#!/usr/bin/env python
"""Render the Esperanto TV station ident (10 s, 1024x576p25) to a single TS segment.

Picture: dark green card, white canton with a geometrically drawn green star
(no font glyph, so it cannot turn into a missing-glyph box), "ESPERANTO / TV".
Sound: the opening soundscape of "Esperanto Estas" part 1 (first 10 s).
Output replaces the between-programme bumper of the Esperanto TV station.
"""
import math, os, subprocess, sys, tempfile
from PIL import Image, ImageDraw, ImageFont, ImageFilter

W, H, S = 1024, 576, 2          # render at 2x, downscale for antialiasing
GREEN_BG = (8, 52, 38)
GREEN_BG2 = (4, 34, 25)
STAR = (0, 153, 0)             # Esperanto flag green
GOLD = (212, 175, 55)
LIGHT = (180, 230, 200)

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
EOTV = os.path.join(ROOT, "pkg/iptv-live-bridge/esperantotv")
OUT = os.path.join(EOTV, "stacia_vineto_0000.ts")
SOUND_SRC = [os.path.join(EOTV, f"dok_estas_parto_01_{i:04d}.ts") for i in (1, 2)]
FONT_LIGHT = "/usr/share/fonts/noto/NotoSans-Light.ttf"
FONT_REG = "/usr/share/fonts/noto/NotoSans-Regular.ttf"


def star_points(cx, cy, r_out, r_in, rot=-math.pi / 2):
    pts = []
    for i in range(10):
        r = r_out if i % 2 == 0 else r_in
        a = rot + i * math.pi / 5
        pts.append((cx + r * math.cos(a), cy + r * math.sin(a)))
    return pts


def layer_background(path):
    im = Image.new("RGB", (W * S, H * S), GREEN_BG)
    d = ImageDraw.Draw(im)
    for y in range(H * S):          # subtle vertical gradient
        t = y / (H * S)
        c = tuple(int(GREEN_BG[i] * (1 - t) + GREEN_BG2[i] * t) for i in range(3))
        d.line([(0, y), (W * S, y)], fill=c)
    m = 28 * S                      # thin double frame
    d.rectangle([m, m, W * S - m, H * S - m], outline=(40, 120, 90), width=2 * S)
    d.rectangle([m + 10 * S, m + 10 * S, W * S - m - 10 * S, H * S - m - 10 * S], outline=(120, 110, 50), width=1 * S)
    im.resize((W, H), Image.LANCZOS).save(path)


def layer_star(path):
    im = Image.new("RGBA", (W * S, H * S), (0, 0, 0, 0))
    d = ImageDraw.Draw(im)
    cx, cy, side = W * S // 2, 190 * S, 120 * S
    box = [cx - side // 2, cy - side // 2, cx + side // 2, cy + side // 2]
    shadow = Image.new("RGBA", im.size, (0, 0, 0, 0))
    ImageDraw.Draw(shadow).rounded_rectangle([b + 6 * S for b in box], 10 * S, fill=(0, 0, 0, 110))
    im = Image.alpha_composite(im, shadow.filter(ImageFilter.GaussianBlur(8 * S)))
    d = ImageDraw.Draw(im)
    d.rounded_rectangle(box, 10 * S, fill=(255, 255, 255, 255))
    d.polygon(star_points(cx, cy + 4 * S, 50 * S, 20 * S), fill=STAR + (255,))
    im.resize((W, H), Image.LANCZOS).save(path)


def layer_text(path):
    im = Image.new("RGBA", (W * S, H * S), (0, 0, 0, 0))
    d = ImageDraw.Draw(im)
    f1 = ImageFont.truetype(FONT_REG, 34 * S)
    f2 = ImageFont.truetype(FONT_LIGHT, 92 * S)
    word, gap = "ESPERANTO", 14 * S   # letter-spaced
    widths = [d.textlength(ch, font=f1) for ch in word]
    total = sum(widths) + gap * (len(word) - 1)
    x, y = (W * S - total) / 2, 290 * S
    for ch, w in zip(word, widths):
        d.text((x, y), ch, font=f1, fill=LIGHT + (255,))
        x += w + gap
    d.line([(W * S / 2 - 150 * S, 348 * S), (W * S / 2 + 150 * S, 348 * S)], fill=GOLD + (255,), width=2 * S)
    tv = "TV"
    d.text(((W * S - d.textlength(tv, font=f2)) / 2, 352 * S), tv, font=f2, fill=(255, 255, 255, 255))
    im.resize((W, H), Image.LANCZOS).save(path)


def layer_standby_text(path):
    im = Image.new("RGBA", (W * S, H * S), (0, 0, 0, 0))
    d = ImageDraw.Draw(im)
    f_title = ImageFont.truetype(FONT_LIGHT, 50 * S)
    f_sub = ImageFont.truetype(FONT_REG, 22 * S)
    f_box = ImageFont.truetype(FONT_REG, 20 * S)
    f_url = ImageFont.truetype(FONT_REG, 16 * S)
    cx = W * S / 2

    def centered(y, text, font, fill):
        d.text((cx - d.textlength(text, font=font) / 2, y), text, font=font, fill=fill)

    centered(275 * S, "ESPERANTO TELEVIDO", f_title, LIGHT + (255,))
    d.line([(cx - 220 * S, 350 * S), (cx + 220 * S, 350 * S)], fill=GOLD + (255,), width=2 * S)
    centered(364 * S, "La Tutmonda Kanalo de Kulturo kaj Komunumo", f_sub, (255, 255, 255, 255))
    bw, bh, by = 300 * S, 44 * S, 420 * S
    d.rectangle([cx - bw / 2, by, cx + bw / 2, by + bh], fill=(12, 20, 16, 255), outline=(40, 160, 110, 255), width=2 * S)
    centered(by + 9 * S, "BONVOLU ATENDI", f_box, LIGHT + (255,))
    centered(500 * S, "kiefte.eu/iptv", f_url, (90, 170, 130, 255))
    im.resize((W, H), Image.LANCZOS).save(path)


def render_standby(tmp):
    """10 s silent 'please wait' card, shown when the station has no schedule."""
    bg, st, tx = (os.path.join(tmp, n) for n in ("bg.png", "star.png", "standby.png"))
    layer_background(bg); layer_star(st); layer_standby_text(tx)
    out = os.path.join(ROOT, "pkg/iptv-live-bridge/testcard/esperanto_standby0.ts")
    fc = "[0:v][1:v]overlay=0:0[a];[a][2:v]overlay=0:0,format=yuv420p[v]"
    subprocess.run(["ffmpeg", "-v", "error", "-y",
                    "-loop", "1", "-framerate", "25", "-t", "10", "-i", bg,
                    "-loop", "1", "-framerate", "25", "-t", "10", "-i", st,
                    "-loop", "1", "-framerate", "25", "-t", "10", "-i", tx,
                    "-f", "lavfi", "-t", "10", "-i", "anullsrc=r=48000:cl=stereo",
                    "-filter_complex", fc, "-map", "[v]", "-map", "3:a", "-t", "10", "-r", "25",
                    "-c:v", "libx264", "-preset", "slow", "-crf", "20", "-tune", "stillimage", "-pix_fmt", "yuv420p", "-g", "250",
                    "-c:a", "aac", "-b:a", "64k", "-f", "mpegts", out], check=True)
    print(out)


def main():
    tmp = tempfile.mkdtemp(prefix="eotv-ident-")
    if "--standby" in sys.argv:
        render_standby(tmp)
        return
    bg, st, tx = (os.path.join(tmp, n) for n in ("bg.png", "star.png", "text.png"))
    layer_background(bg); layer_star(st); layer_text(tx)
    dur = 10.0
    fc = (
        f"[0:v]format=yuv420p[bg];"
        f"[1:v]format=rgba,fade=t=in:st=0.4:d=0.8:alpha=1[st];"
        f"[2:v]format=rgba,fade=t=in:st=1.4:d=0.9:alpha=1[tx];"
        f"[bg][st]overlay=0:0[a];[a][tx]overlay=0:0,fade=t=out:st={dur-0.8}:d=0.8,format=yuv420p[v];"
        f"[3:a]atrim=0:{dur},asetpts=PTS-STARTPTS,afade=t=in:d=0.3,afade=t=out:st={dur-2.0}:d=2.0,"
        f"aresample=48000,aformat=channel_layouts=stereo[aud]"
    )
    cmd = ["ffmpeg", "-v", "error", "-y",
           "-loop", "1", "-framerate", "25", "-t", str(dur), "-i", bg,
           "-loop", "1", "-framerate", "25", "-t", str(dur), "-i", st,
           "-loop", "1", "-framerate", "25", "-t", str(dur), "-i", tx,
           "-i", "concat:" + "|".join(SOUND_SRC),
           "-filter_complex", fc, "-map", "[v]", "-map", "[aud]",
           "-t", str(dur), "-r", "25",
           "-c:v", "libx264", "-preset", "slow", "-crf", "18", "-pix_fmt", "yuv420p", "-g", "250",
           "-c:a", "aac", "-b:a", "128k", "-ar", "48000",
           "-mpegts_flags", "initial_discontinuity", "-f", "mpegts", OUT]
    subprocess.run(cmd, check=True)
    print(OUT)


if __name__ == "__main__":
    main()
