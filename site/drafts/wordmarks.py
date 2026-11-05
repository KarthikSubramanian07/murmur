"""Build the wordmark variants of classic-tex and signal-tex.

Usage: python -I gen.py <site/drafts dir>
"""
import re, sys, pathlib

D = pathlib.Path(sys.argv[1])

SAW = ("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='2560' height='1600'%3E%3Cdefs%3E"
       "%3ClinearGradient id='r'%3E%3Cstop offset='0' stop-color='rgb(0,128,0)'/%3E%3Cstop offset='1' "
       "stop-color='rgb(255,128,0)'/%3E%3C/linearGradient%3E%3Cpattern id='p' width='12' height='1600' "
       "patternUnits='userSpaceOnUse'%3E%3Crect width='12' height='1600' fill='url(%23r)'/%3E%3C/pattern%3E"
       "%3C/defs%3E%3Crect width='2560' height='1600' fill='url(%23p)'/%3E%3C/svg%3E")

LENS = SAW.replace("%3Cstop offset='1' stop-color='rgb(255,128,0)'/%3E",
                   "%3Cstop offset='.5' stop-color='rgb(255,128,0)'/%3E%3Cstop offset='1' stop-color='rgb(0,128,0)'/%3E")
assert LENS != SAW

def reed_filter(fid, blur, scale, img=None):
    img = img or SAW
    return (f'<filter id="{fid}" filterUnits="userSpaceOnUse" x="0" y="0" width="100%" height="100%" '
            f'color-interpolation-filters="sRGB"><feGaussianBlur in="SourceGraphic" stdDeviation="{blur}" result="b"/>'
            f'<feImage href="{img}" x="0" y="0" width="2560" height="1600" preserveAspectRatio="none" result="saw"/>'
            f'<feDisplacementMap in="b" in2="saw" scale="{scale}" xChannelSelector="R" yChannelSelector="G"/></filter>')

P = {
    'classic': dict(
        line1='#FFFBEA', line2='#FDEE8C', ink='14, 27, 69',
        ground=[('0', '#3369FF'), ('.55', '#2A61FF'), ('1', '#225BFF')],
        tone='#FFFBEA', tone_op='.34',
    ),
    'signal': dict(
        line1='#FFFFFF', line2='#4DCADF', ink='2, 28, 43',
        ground=[('0', '#02679A'), ('.55', '#015F8D'), ('1', '#014A6E')],
        tone='#4DCADF', tone_op='.34',
    ),
}

WIDE_RE = re.compile(r'<g class="wide-g" filter="url\(#reed\)">(.*?)</g>')
NARROW_RE = re.compile(r'<g class="narrow" filter="url\(#reed-n\)">(.*?)</g>')


def add_css(html, css):
    return html.replace('</style>', css + '\n</style>', 1)


# ---------- 1. Refracted: the name is a light behind the glass ----------
def refract(html, p):
    defs = (reed_filter('reed-t', '4 1.2', 14, LENS) + reed_filter('reed-tn', '3 1', 12, LENS)
            + reed_filter('reed-tc', '1 0.3', 8, LENS) + reed_filter('reed-tcn', '0.8 0.3', 8, LENS))
    html = html.replace('</filter></defs>', '</filter>' + defs + '</defs>', 1)
    def word(cls, glow, core):
        # A wide glow of the second color, and a sharper core of the first, both behind the reeds.
        return (f'<g class="{cls}"><g filter="url(#{glow})"><text class="word" text-anchor="middle" x="50%" y="0" '
                f'fill="{p["line2"]}" fill-opacity=".8">murmur</text></g>'
                f'<g filter="url(#{core})"><text class="word" text-anchor="middle" x="50%" y="0" '
                f'fill="{p["line1"]}" fill-opacity=".8">murmur</text></g></g>')
    html = WIDE_RE.sub(lambda m: m.group(0) + word('wide-g', 'reed-t', 'reed-tc'), html, 1)
    html = NARROW_RE.sub(lambda m: m.group(0) + word('narrow', 'reed-tn', 'reed-tcn'), html, 1)
    css = '''
  /* ---------- Wordmark: "murmur", lit behind the glass ---------- */
  /* The name is a light in the same layer as the figure: a wide glow and a sharper core, each sent
     through the reeds by a filter whose map is a lens per reed (a triangle ramp, not the sawtooth),
     so the letters ripple rather than shatter. It spans the top of the hero; the corner cards float
     over it on the glass, and the headline steps down a little to clear it. */
  .glass .word { font: 700 19vw/1 Inter, system-ui, sans-serif; letter-spacing: -.06em; transform: translateY(330px); }
  .center { margin-top: 190px; }
  .cards { --drop: max(120px, 50% - 330px); }
  @media (max-width: 1099px) {
    .center { margin-top: 0; padding-top: calc(12vw + 64px); }
    .glass .word { font-size: 22vw; transform: translateY(calc(141px + 12vw)); }
  }'''
    return add_css(html, css)


# ---------- 2. Cut glass: the name is cut out of the fluted pane ----------
def cut(html, p):
    ink = p['ink']
    grad = ''.join(f'<stop offset="{o}" stop-color="{c}"/>' for o, c in p['ground'])
    defs = (
        f'<linearGradient id="gnd" x1="0" y1="0" x2="0" y2="1">{grad}</linearGradient>'
        '<linearGradient id="fg" x1="0" x2="1">'
        '<stop offset="0" stop-color="#fff" stop-opacity=".16"/><stop offset=".083" stop-color="#fff" stop-opacity=".05"/>'
        '<stop offset=".417" stop-color="#fff" stop-opacity="0"/>'
        f'<stop offset=".75" stop-color="rgb({ink})" stop-opacity=".05"/><stop offset=".917" stop-color="rgb({ink})" stop-opacity=".22"/>'
        f'<stop offset="1" stop-color="rgb({ink})" stop-opacity=".3"/></linearGradient>'
        '<pattern id="flutes" width="12" height="16" patternUnits="userSpaceOnUse"><rect width="12" height="16" fill="url(#fg)"/></pattern>'
        '<filter id="soft" filterUnits="userSpaceOnUse" x="0" y="0" width="100%" height="100%"><feGaussianBlur stdDeviation="64 14"/></filter>'
        '<filter id="soft-n" filterUnits="userSpaceOnUse" x="0" y="0" width="100%" height="100%"><feGaussianBlur stdDeviation="34 10"/></filter>'
        '<filter id="bevel" x="-5%" y="-5%" width="110%" height="110%"><feGaussianBlur stdDeviation="2.5"/></filter>'
        '<clipPath id="cw"><text class="cut" x="50%" y="338" text-anchor="middle">MURMUR</text></clipPath>'
        '<clipPath id="cn"><text class="cut-n" x="50%" y="0" text-anchor="middle">MURMUR</text></clipPath>'
        '<mask id="mw" maskUnits="userSpaceOnUse" x="0" y="0" width="100%" height="100%"><rect width="100%" height="100%" fill="#fff"/>'
        '<text class="cut" x="50%" y="338" text-anchor="middle" fill="#000">MURMUR</text></mask>'
        '<mask id="mn" maskUnits="userSpaceOnUse" x="0" y="0" width="100%" height="100%"><rect width="100%" height="100%" fill="#fff"/>'
        '<text class="cut-n" x="50%" y="0" text-anchor="middle" fill="#000">MURMUR</text></mask>'
    )
    html = html.replace('</filter></defs>', '</filter>' + defs + '</defs>', 1)

    def pane(inner, reed, soft, clip, mask, cls, tcls, y):
        return (
            f'<g class="{cls}">'
            # Ground, so the reeds blend with it inside this SVG.
            '<rect width="100%" height="100%" fill="url(#gnd)"/>'
            # Through the reeds: the light, refracted.
            f'<g filter="url(#{reed})">{inner}</g>'
            # Through the letters: the same light, blurred but not refracted, on clean ground.
            f'<g clip-path="url(#{clip})"><rect width="100%" height="100%" fill="url(#gnd)"/>'
            f'<g filter="url(#{soft})">{inner}</g>'
            '<rect width="100%" height="100%" fill="#fff" fill-opacity=".09"/>'
            # Inner bevel: the cut edge catches light on its upper left and shades on its lower right.
            f'<text class="{tcls}" x="50%" y="{y}" text-anchor="middle" fill="none" stroke="rgb({ink})" stroke-opacity=".45" '
            'stroke-width="8" transform="translate(4 5)" filter="url(#bevel)">MURMUR</text>'
            f'<text class="{tcls}" x="50%" y="{y}" text-anchor="middle" fill="none" stroke="#fff" stroke-opacity=".5" '
            'stroke-width="5" transform="translate(-2 -2)" filter="url(#bevel)">MURMUR</text></g>'
            # The reed faces, everywhere but the letters.
            f'<rect width="100%" height="100%" fill="url(#flutes)" mask="url(#{mask})" style="mix-blend-mode:soft-light"/>'
            # A hairline on the cut edge.
            f'<text class="{tcls}" x="50%" y="{y}" text-anchor="middle" fill="none" stroke="#fff" stroke-opacity=".28" stroke-width="1"/>'
            '</g>'
        ).replace(f'<text class="{tcls}" x="50%" y="{y}" text-anchor="middle" fill="none" stroke="#fff" stroke-opacity=".28" stroke-width="1"/>',
                  f'<text class="{tcls}" x="50%" y="{y}" text-anchor="middle" fill="none" stroke="#fff" stroke-opacity=".28" stroke-width="1">MURMUR</text>')

    html = WIDE_RE.sub(lambda m: pane(m.group(1), 'reed', 'soft', 'cw', 'mw', 'wide-g', 'cut', '338'), html, 1)
    html = NARROW_RE.sub(lambda m: pane(m.group(1), 'reed-n', 'soft-n', 'cn', 'mn', 'narrow', 'cut-n', '0'), html, 1)
    css = '''
  /* ---------- Wordmark: "MURMUR" cut out of the glass ---------- */
  /* The letters are clear panes in the fluted glass: inside them the light is blurred but not
     refracted and there are no reeds, and each cut edge has a bevel and a hairline. The reeds are
     drawn in the SVG here, not by .glass::after, so they can stop at the letters. */
  .glass::after { display: none; }
  .glass .cut { font: 700 15vw/1 Inter, system-ui, sans-serif; letter-spacing: -.045em; }
  .center { margin-top: 190px; }
  .cards { --drop: max(120px, 50% - 330px); }
  @media (max-width: 1099px) { .center { margin-top: 0; } }
  .glass .cut-n { font: 700 19vw/1 Inter, system-ui, sans-serif; letter-spacing: -.045em; transform: translateY(calc(var(--top, 150px) + 14vw)); }
  @media (max-width: 1099px) { .center { padding-top: calc(14vw + 48px); } .glass { --top: 150px; } }'''
    return add_css(html, css)


# ---------- 3. Surface: the name leads the headline, half under the glass ----------
def surface(html, p):
    filt = ('<svg width="0" height="0" style="position:absolute" aria-hidden="true"><defs>'
            '<filter id="reed-w" x="-4%" y="-10%" width="108%" height="120%" color-interpolation-filters="sRGB">'
            '<feGaussianBlur in="SourceGraphic" stdDeviation="1.2 0.3" result="b"/>'
            f'<feImage href="{LENS}" x="0" y="0" width="2560" height="1600" preserveAspectRatio="none" result="saw"/>'
            '<feDisplacementMap in="b" in2="saw" scale="6" xChannelSelector="R" yChannelSelector="G" result="d"/><feGaussianBlur in="d" stdDeviation="0.7 0.5"/></filter></defs></svg>')
    mark = ('<div class="mark"><span class="above">murmur</span>'
            '<span class="below" aria-hidden="true">murmur</span></div>\n    ')
    html = html.replace('<div class="center">\n    ', '<div class="center">\n    ' + mark, 1)
    html = html.replace('<main class="page">', '<main class="page">\n  ' + filt, 1)
    css = f'''
  /* ---------- Wordmark: "murmur" at the surface of the glass ---------- */
  /* The name leads the hero. Its upper half stands clear of the glass in the first line's color;
     its lower half is under it, refracted by the same 12px reeds, in the second line's color, so
     the two halves echo the headline's two lines. A hairline marks the glass edge. The headline
     steps down a size to make room, and the cards below drop to clear it. */
  .mark {{ position: relative; display: inline-block; font: 700 clamp(84px, 11.5vw, 164px)/1 Inter, system-ui, sans-serif;
    letter-spacing: -.065em; margin: 0 0 26px; padding: 0 .06em 0 0; }}
  .mark span {{ display: block; }}
  .mark .above {{ color: {p['line1']}; clip-path: inset(-10% -10% 41% -10%); }}
  .mark .below {{ position: absolute; inset: 0 .06em 0 0; color: {p['line2']}; filter: url(#reed-w); clip-path: inset(59% -10% -10% -10%); opacity: .92; }}
  .mark::after {{ content: ""; position: absolute; left: -12%; right: -12%; top: 59%; height: 1px;
    background: linear-gradient(90deg, transparent, rgba(255, 255, 255, .55) 22%, rgba(255, 255, 255, .55) 78%, transparent); }}
  h1 {{ font-size: clamp(min(30px, 8.5vw), 3.5vw, 50px); }}
  .center p {{ margin-top: 22px; }}
  .button {{ margin-top: 30px; }}
  .cards {{ --drop: max(84px, 50% - 366px); }}
  @media (max-width: 1099px) {{ .mark {{ font-size: 23vw; }} }}'''
    return add_css(html, css)


VARIANTS = {'refract': refract, 'cut': cut, 'surface': surface}

for pal in ('classic', 'signal'):
    base = (D / f'{pal}-tex' / 'index.html').read_text(encoding='utf-8')
    assert WIDE_RE.search(base) and NARROW_RE.search(base)
    for name, fn in VARIANTS.items():
        out = fn(base, P[pal])
        assert out != base
        d = D / f'{pal}-tex-{name}'
        d.mkdir(exist_ok=True)
        (d / 'index.html').write_text(out, encoding='utf-8', newline='')
        print(d.name)
