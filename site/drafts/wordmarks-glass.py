"""Builds the five glass drafts from the two calm bases. Run: python gen.py"""
import re, pathlib

D = pathlib.Path(r"C:\Users\smash\Desktop\Claude Work Space\murmur-drafts\site\drafts")


def read(name):
    with open(D / name / "index.html", encoding="utf-8", newline="") as f:
        return f.read()


def write(name, text):
    (D / name).mkdir(exist_ok=True)
    with open(D / name / "index.html", "w", encoding="utf-8", newline="") as f:
        f.write(text)


GLASS_RE = re.compile(r'  <div class="glass" aria-hidden="true">.*?</div>\n', re.S)


def glass_of(html):
    return GLASS_RE.search(html).group(0)


def build(base, name, css, glass_fn=None):
    html = read(base)
    if glass_fn:
        g = glass_of(html)
        ng = glass_fn(g)
        assert ng != g
        html = html.replace(g, ng)
    assert html.count("</style>") == 1
    html = html.replace("</style>", css.strip("\n") + "\n</style>")
    write(name, html)


def swap_group(g, cls, filt, inner):
    """Replace the shapes inside <g class=cls filter=url(#filt)>…</g>."""
    pat = re.compile(r'(<g class="%s" filter="url\(#%s\)">).*?(</g>)' % (cls, filt))
    out, n = pat.subn(lambda m: m.group(1) + inner + m.group(2), g)
    assert n == 1, (cls, filt)
    return out


# ---------------------------------------------------------------- 1. horizon
HORIZON_DEFS = (
    '<linearGradient id="haze" x1="0" y1="0" x2="0" y2="1">'
    '<stop offset="0" stop-color="#91ADFF" stop-opacity="0"/>'
    '<stop offset=".8" stop-color="#91ADFF" stop-opacity=".42"/>'
    '<stop offset="1" stop-color="#91ADFF" stop-opacity="0"/></linearGradient>'
)
HORIZON_WIDE = (
    # the sky the sunrise lifts, fading up from the band
    '<rect x="-10%" y="64%" width="120%" height="26%" fill="url(#haze)"/>'
    # the band itself: thin, full width, brightest in the middle
    '<ellipse cx="50%" cy="85.5%" rx="76%" ry="2.2%" fill="#FDEE8C" opacity="0.9"/>'
    '<ellipse cx="50%" cy="85.5%" rx="32%" ry="1.1%" fill="#FFF6C4"/>'
)
HORIZON_NARROW = (
    '<rect x="-10%" y="74%" width="120%" height="20%" fill="url(#haze)"/>'
    '<ellipse cx="50%" cy="90%" rx="90%" ry="1.3%" fill="#FDEE8C" opacity="0.9"/>'
    '<ellipse cx="50%" cy="90%" rx="40%" ry="0.7%" fill="#FFF6C4"/>'
)


def widen(g, fid):
    old = f'<filter id="{fid}" filterUnits="userSpaceOnUse" x="0" y="0" width="100%" height="100%"'
    assert old in g
    return g.replace(old, f'<filter id="{fid}" filterUnits="userSpaceOnUse" x="-6%" y="0" width="112%" height="100%"')


def horizon_glass(g):
    g = g.replace("<defs>", "<defs>" + HORIZON_DEFS, 1)
    g = widen(widen(g, "reed"), "reed-n")
    g = swap_group(g, "wide-g", "reed", HORIZON_WIDE)
    g = swap_group(g, "narrow", "reed-n", HORIZON_NARROW)
    return g


HORIZON_CSS = """
  /* Horizon: the light behind the glass is one thin butter band low across the whole width, a
     sunrise seen through the reeds. The ground deepens toward ink at the top, so the band reads as
     the brightest thing on the page after the word. The glass fades out lower than before, below
     the band, so the band is not cut by the fade. */
  .page { background: linear-gradient(180deg,
      color-mix(in srgb, var(--blue) 46%, var(--ink)) 0%,
      color-mix(in srgb, var(--blue) 70%, var(--ink)) 34%,
      color-mix(in srgb, var(--blue) 94%, var(--ink)) 66%,
      var(--blue) 86%); }
  .page .glass { -webkit-mask-image: linear-gradient(180deg, #000 90%, transparent 100%); mask-image: linear-gradient(180deg, #000 90%, transparent 100%); }
  @media (max-width: 1099px) {
    .page .glass { -webkit-mask-image: linear-gradient(180deg, #000 94%, transparent 100%); mask-image: linear-gradient(180deg, #000 94%, transparent 100%); }
  }
"""

# ---------------------------------------------------------------- 2. beam
BEAM_DEFS = (
    '<linearGradient id="beam-v" x1="0" y1="0" x2="0" y2="1">'
    '<stop offset=".08" stop-color="#FFFBEA" stop-opacity="0"/>'
    '<stop offset=".5" stop-color="#FFFBEA" stop-opacity="0.5"/>'
    '<stop offset=".86" stop-color="#FFFBEA" stop-opacity="0.12"/>'
    '<stop offset="1" stop-color="#FFFBEA" stop-opacity="0"/></linearGradient>'
    '<linearGradient id="beam-c" x1="0" y1="0" x2="0" y2="1">'
    '<stop offset=".08" stop-color="#FDEE8C" stop-opacity="0"/>'
    '<stop offset=".52" stop-color="#FDEE8C" stop-opacity="0.8"/>'
    '<stop offset=".86" stop-color="#FDEE8C" stop-opacity="0.1"/>'
    '<stop offset="1" stop-color="#FDEE8C" stop-opacity="0"/></linearGradient>'
)
BEAM_WIDE = (
    '<rect x="42.5%" y="0" width="15%" height="44%" fill="url(#beam-v)"/>'
    '<rect x="47.4%" y="0" width="5.2%" height="43%" fill="url(#beam-c)"/>'
)
BEAM_NARROW = (
    '<rect x="28%" y="8%" width="44%" height="34%" fill="url(#beam-v)"/>'
    '<rect x="41%" y="8%" width="18%" height="32%" fill="url(#beam-c)"/>'
)


def beam_glass(g):
    g = g.replace("<defs>", "<defs>" + BEAM_DEFS, 1)
    g = swap_group(g, "wide-g", "reed", BEAM_WIDE)
    g = swap_group(g, "narrow", "reed-n", BEAM_NARROW)
    return g


BEAM_CSS = """
  /* Beam: the only light behind the glass is one soft column of cream and butter, straight up
     behind the word; the reeds break it into strips. The rest is plain blue glass. */
"""

# ---------------------------------------------------------------- 3. wide reeds
W = 28  # reed period, px
K = W / 12


def wide_glass(g):
    # period of every map
    g2 = g.replace("width='12' height='1600'", f"width='{W}' height='1600'")
    assert g2.count(f"width='{W}' height='1600'") == 12
    # displacement scales, scaled with the period so each reed bends the light as far, relatively
    scales = {"reed": 34, "reed-n": 26, "reed-t": 14, "reed-tn": 12, "reed-tc": 8, "reed-tcn": 8}
    blurs = {"reed": "64 14", "reed-n": "34 10", "reed-t": "4 1.2", "reed-tn": "3 1",
             "reed-tc": "1 0.3", "reed-tcn": "0.8 0.3"}
    new_scale = {"reed": 64, "reed-n": 50, "reed-t": 20, "reed-tn": 14, "reed-tc": 11, "reed-tcn": 9}
    new_blur = {"reed": "72 14", "reed-n": "40 10", "reed-t": "7 1.4", "reed-tn": "5 1.2",
                "reed-tc": "1.6 0.4", "reed-tcn": "1.2 0.4"}
    for fid in scales:
        pat = re.compile(r'(<filter id="%s" .*?</filter>)' % re.escape(fid))
        m = pat.search(g2)
        f = m.group(1)
        f2 = f.replace(f'stdDeviation="{blurs[fid]}"', f'stdDeviation="{new_blur[fid]}"')
        f2 = f2.replace(f'scale="{scales[fid]}"', f'scale="{new_scale[fid]}"')
        assert f2.count(new_blur[fid]) == 1 and f2.count(f'scale="{new_scale[fid]}"') == 1, fid
        g2 = g2.replace(f, f2)
    return g2


WIDE_CSS = """
  /* Wide reeds: 28px instead of 12, softer. The refraction maps, the filter scales and these reed
     faces all use the same 28px period, so the faces sit on the strips the light is bent in. The
     faces are the 12px ones stretched, with gentler hairlines. Only the hero's glass uses them; the
     film and page 2 keep the 12px reeds. */
  .page .glass::after { background: repeating-linear-gradient(90deg,
      rgba(255, 255, 255, 0.13) 0px,
      rgba(255, 255, 255, 0.05) 2px,
      rgba(255, 255, 255, 0) 11px,
      rgba(14, 27, 69, 0.04) 20px,
      rgba(14, 27, 69, 0.16) 26px,
      rgba(14, 27, 69, 0.24) 28px); }
"""

# ---------------------------------------------------------------- 4. wave
REGION = 'filterUnits="userSpaceOnUse" x="-10%" y="-10%" width="120%" height="120%" color-interpolation-filters="sRGB"'
OX, OY, MW, MH = -240, -240, 2880, 2000  # map placement; -240 keeps the period's phase at x=0


def map_uri(period, tri):
    stops = ("%3Cstop offset='0' stop-color='rgb(0,128,0)'/%3E%3Cstop offset='.5' stop-color='rgb(255,128,0)'/%3E%3Cstop offset='1' stop-color='rgb(0,128,0)'/%3E"
             if tri else
             "%3Cstop offset='0' stop-color='rgb(0,128,0)'/%3E%3Cstop offset='1' stop-color='rgb(255,128,0)'/%3E")
    return ("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='{W}' height='{H}'%3E%3Cdefs%3E"
            "%3ClinearGradient id='r'%3E{S}%3C/linearGradient%3E%3Cpattern id='p' width='{P}' height='{H}' patternUnits='userSpaceOnUse'%3E"
            "%3Crect width='{P}' height='{H}' fill='url(%23r)'/%3E%3C/pattern%3E%3C/defs%3E%3Crect width='{W}' height='{H}' fill='url(%23p)'/%3E%3C/svg%3E"
            ).format(W=MW, H=MH, P=period, S=stops)


# One low-frequency noise, the same in every filter (same region, same seed), bends the map.
WAVE_FREQ = "0.0008 0.0017"
WAVE_SCALE = 110


def bent_map(tri):
    return (f'<feImage href="{map_uri(12, tri)}" x="{OX}" y="{OY}" width="{MW}" height="{MH}" preserveAspectRatio="none" result="m0"/>'
            f'<feTurbulence type="fractalNoise" baseFrequency="{WAVE_FREQ}" numOctaves="1" seed="7" result="wob"/>'
            f'<feDisplacementMap in="m0" in2="wob" scale="{WAVE_SCALE}" xChannelSelector="R" yChannelSelector="G" result="saw"/>')


def wave_filter(fid, blur, tri, scale):
    return (f'<filter id="{fid}" {REGION}><feGaussianBlur in="SourceGraphic" stdDeviation="{blur}" result="b"/>'
            + bent_map(tri) +
            f'<feDisplacementMap in="b" in2="saw" scale="{scale}" xChannelSelector="R" yChannelSelector="G"/></filter>')


def table(vals):
    return " ".join(("%.3f" % v).rstrip("0").rstrip(".") or "0" for v in vals)


# Reed faces from the bent sawtooth itself: its red channel is the position across each reed
# (0 at the left edge, 1 at the right), so a lookup on it draws the same hairlines --flutes does.
HI = [.16, .05, .04, .03, .015, 0, 0, 0, 0, 0, 0, 0, 0]
LO = [0, 0, 0, 0, 0, 0, .0125, .025, .0375, .05, .135, .22, .3]
FLUTE_FILTER = (
    f'<filter id="flute" {REGION}>' + bent_map(False) +
    '<feColorMatrix in="saw" type="matrix" values="0 0 0 0 1  0 0 0 0 1  0 0 0 0 1  1 0 0 0 0" result="h0"/>'
    f'<feComponentTransfer in="h0" result="hi"><feFuncA type="table" tableValues="{table(HI)}"/></feComponentTransfer>'
    '<feColorMatrix in="saw" type="matrix" values="0 0 0 0 .055  0 0 0 0 .106  0 0 0 0 .271  1 0 0 0 0" result="l0"/>'
    f'<feComponentTransfer in="l0" result="lo"><feFuncA type="table" tableValues="{table(LO)}"/></feComponentTransfer>'
    '<feMerge result="f"><feMergeNode in="hi"/><feMergeNode in="lo"/></feMerge>'
    '<feGaussianBlur in="f" stdDeviation="0.6 0"/></filter>'
)


def wave_glass(g):
    defs = "".join([
        wave_filter("reed", "64 14", False, 34),
        wave_filter("reed-n", "34 10", False, 26),
        wave_filter("reed-t", "4 1.2", True, 14),
        wave_filter("reed-tn", "3 1", True, 12),
        wave_filter("reed-tc", "1 0.3", True, 8),
        wave_filter("reed-tcn", "0.8 0.3", True, 8),
        FLUTE_FILTER,
    ])
    g2 = re.sub(r"<defs>.*?</defs>", lambda m: "<defs>" + defs + "</defs>", g, flags=re.S)
    g2 = g2.replace("</svg>", '<rect class="faces" x="0" y="0" width="100%" height="100%" fill="#000" filter="url(#flute)"/></svg>')
    return g2


WAVE_CSS = """
  /* Wave: rippled glass. One slow noise field bends every reed a little, so the strips sway
     instead of running dead straight. The light, the word and the reed faces are all sent through
     the same bent map; the faces are drawn in the SVG from that map (the straight --flutes overlay
     could not follow the bend), and blend as the overlay did. */
  .page .glass::after { display: none; }
  .page .glass .faces { mix-blend-mode: soft-light; }
"""

# ---------------------------------------------------------------- 5. frost
FROST_CSS = """
  /* Frost: one band of frosted glass runs across the hero behind the headline and the line under
     it. It blurs what is behind it, so the reeds stop there, and is a shade lighter, with a finer,
     stronger grain of its own (light and dark specks as alpha, so no blend mode is needed: a blend
     mode on a sibling would cut the band off from the glass it has to blur). Above it (the word)
     and below it (the button, the glow) the glass stays reeded. */
  .page .center::before {
    content: ""; position: absolute; z-index: -1; pointer-events: none;
    left: 50%; width: 100vw; margin-left: -50vw;
    top: -26px; bottom: calc(36px + 50px - 22px);
    -webkit-backdrop-filter: blur(18px); backdrop-filter: blur(18px);
    background:
      url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='160' height='160'%3E%3Cfilter id='n'%3E%3CfeTurbulence type='fractalNoise' baseFrequency='1.1' numOctaves='2' stitchTiles='stitch'/%3E%3CfeColorMatrix values='0 0 0 0 1 0 0 0 0 1 0 0 0 0 1 1.5 0 0 0 -.68'/%3E%3C/filter%3E%3Cfilter id='d'%3E%3CfeTurbulence type='fractalNoise' baseFrequency='1.1' numOctaves='2' seed='9' stitchTiles='stitch'/%3E%3CfeColorMatrix values='0 0 0 0 .055 0 0 0 0 .106 0 0 0 0 .271 1.5 0 0 0 -.68'/%3E%3C/filter%3E%3Crect width='100%25' height='100%25' filter='url(%23n)'/%3E%3Crect width='100%25' height='100%25' filter='url(%23d)'/%3E%3C/svg%3E") 0 0 / 160px 160px,
      color-mix(in srgb, var(--cream) 10%, transparent);
    box-shadow: inset 0 1px 0 color-mix(in srgb, var(--cream) 26%, transparent),
                inset 0 -1px 0 color-mix(in srgb, var(--ink) 16%, transparent);
  }
  @media (max-width: 1099px) {
    .page .center::before { top: calc(12vw + 64px - 30px); bottom: calc(30px + 50px - 18px); }
  }
"""

if __name__ == "__main__":
    build("classic-calm-type", "classic-calm-horizon", HORIZON_CSS, horizon_glass)
    build("classic-calm-messages", "classic-calm-beam", BEAM_CSS, beam_glass)
    build("classic-calm-type", "classic-calm-wide-reeds", WIDE_CSS, wide_glass)
    build("classic-calm-messages", "classic-calm-wave", WAVE_CSS, wave_glass)
    build("classic-calm-type", "classic-calm-frost", FROST_CSS)
    print("ok")
