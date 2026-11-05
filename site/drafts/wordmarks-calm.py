"""Calmer versions of classic-tex-refract: each takes more away.

Usage: python -I wordmarks-calm.py <site/drafts dir>   (run wordmarks.py first)
"""
import re, sys, pathlib

D = pathlib.Path(sys.argv[1])
base = (D / 'classic-tex-refract' / 'index.html').read_text(encoding='utf-8')

FIG_W = re.compile(r'(<g class="wide-g" filter="url\(#reed\)">)(.*?)(</g>)')
FIG_N = re.compile(r'(<g class="narrow" filter="url\(#reed-n\)">)(.*?)(</g>)')

BUTTER, HALO, HI, DEEP = '#FDEE8C', '#91ADFF', '#FFF6C4', '#1640BF'


def ell(cx, cy, rx, ry, fill, op=None):
    o = f' opacity="{op}"' if op else ''
    return f'<ellipse cx="{cx}%" cy="{cy}%" rx="{rx}%" ry="{ry}%" fill="{fill}"{o}/>'


# The light behind the glass, from most to least.
FIGS = {
    # The original figure without the dark shapes in the middle, which muddied the word.
    'clear': (None, ['<ellipse cx="50%" cy="32%"', '<ellipse cx="42%" cy="62%"', '<ellipse cx="53%" cy="74%"']),
    # One butter figure low on the left, one cool shadow on the right.
    'messages': ([ell(14, 76, 18, 16, HALO, .7), ell(15, 76, 11, 11, BUTTER), ell(13, 70, 15, 3.5, BUTTER, .7),
                  ell(14, 76, 5.5, 5.5, HI), ell(86, 70, 9, 16, DEEP, .8)],
                 [ell(14, 66, 40, 6, HALO, .6), ell(16, 66, 34, 3.5, BUTTER), ell(12, 66, 14, 2, HI)]),
    # A single low glow.
    'type': ([ell(16, 84, 20, 12, HALO, .6), ell(16, 86, 13, 7, BUTTER, .9), ell(15, 86, 5, 3.5, HI)],
             [ell(18, 80, 40, 5, HALO, .5), ell(18, 81, 30, 3, BUTTER, .9)]),
    # Nothing but the glass.
    'whisper': ([], []),
}

CSS = {
    'clear': '''
  /* Calm: the corner cards go, and the dark shapes behind the word. The word rises into the room. */
  .c-finch, .c-wren { display: none; }
  .glass .word { transform: translateY(280px); }
  .center { margin-top: 120px; }
  .cards { --drop: max(84px, 50% - 366px); }''',
    'messages': '''
  /* Calm: only the two messages stay, under the button; the sessions are implied by them. */
  .c-finch, .c-wren, .c-otter, .c-heron { display: none; }
  .glass .word { transform: translateY(280px); }
  .center { margin-top: 120px; }
  .cards { --drop: max(84px, 50% - 366px); }
  @media (max-width: 1099px) { .c-reply { align-self: flex-end; } }''',
    'type': '''
  /* Calm: type only. The word, the headline, the line under it and the button, on the glass. */
  .cards { display: none; }
  .glass .word { transform: translateY(max(330px, calc(50vh - 120px))); }
  .center { margin-top: 180px; }
  @media (max-width: 1099px) { .page { padding-bottom: 96px; } }''',
    'whisper': '''
  /* Calm: the word drops to a tint of the ground, so the headline leads and the name is felt more
     than read. No light behind the glass; the two messages stay. */
  .c-finch, .c-wren, .c-otter, .c-heron { display: none; }
  .glass .word { transform: translateY(280px); }
  .center { margin-top: 120px; }
  .cards { --drop: max(84px, 50% - 366px); }''',
}

for name, (shapes, css) in ((k, (FIGS[k], CSS[k])) for k in FIGS):
    html = base
    wide, narrow = shapes
    if wide is None:  # drop listed shapes from the original figure
        def strip(m, drop=narrow):
            inner = m.group(2)
            for start in drop:
                inner = re.sub(re.escape(start) + r'[^>]*/>', '', inner)
            return m.group(1) + inner + m.group(3)
        html = FIG_W.sub(strip, html, 1)
        html = FIG_N.sub(lambda m: strip(m, ['<ellipse cx="64%" cy="22%"', '<ellipse cx="82%" cy="60%"']), html, 1)
    else:
        html = FIG_W.sub(lambda m: m.group(1) + ''.join(wide) + m.group(3), html, 1)
        html = FIG_N.sub(lambda m: m.group(1) + ''.join(narrow) + m.group(3), html, 1)
    if name == 'whisper':
        # No glow; the core goes to a faint tint of cream.
        html = html.replace('fill="#FDEE8C" fill-opacity=".8">murmur', 'fill="#FDEE8C" fill-opacity="0">murmur')
        html = html.replace('fill="#FFFBEA" fill-opacity=".8">murmur', 'fill="#FFFBEA" fill-opacity=".26">murmur')
        assert html.count('fill-opacity=".26">murmur') == 2
    html = html.replace('</style>', css + '\n</style>', 1)
    assert html != base
    d = D / f'classic-calm-{name}'
    d.mkdir(exist_ok=True)
    (d / 'index.html').write_text(html, encoding='utf-8', newline='')
    print(d.name)
