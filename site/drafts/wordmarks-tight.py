"""Tight versions of classic-calm-butter-word and classic-calm-beam, with plain glass behind the word.

The word, headline, line, button and bubbles become one compact group centred on the screen, so
the word sits lower. Only the hero changes.

Usage: python -I wordmarks-tight.py <site/drafts dir>
"""
import re, sys, pathlib

D = pathlib.Path(sys.argv[1])

# On a wide screen the word (in the glass SVG, absolutely placed) has to land just above the copy,
# which is now in normal flow, centred as a column. The group's height is estimated from the same
# sizes the CSS uses: the word's x-height (.546 of 16vw), the 18px gap, the two-line headline
# (2 x 1.05 x its font size), and about 254px for the line, the button and the bubbles.
CSS = '''
  /* ---------- Tight: one compact group, centred, so the word sits lower ---------- */
  @media (min-width: 1100px) {
    .page { flex-direction: column; justify-content: center; }
    .glass .word { font-size: 16vw; transform: translateY(calc(max(410px, 50vh) + 4.37vw - 136px - 1.05 * min(74px, 5.4vw))); }
    .center { margin-top: 0; padding-top: calc(8.74vw + 18px); }
    .center p { margin-top: 18px; }
    .page .button { margin-top: 24px; }
    .cards { position: relative; z-index: 1; display: flex; justify-content: center; gap: 20px; margin-top: 22px; }
    .cards .card { position: static; }
    .cards .message { width: 300px; }
    .cards .c-reply { margin-top: 14px; }
  }
  @media (max-width: 1099px) {
    .page { padding-top: 48px; }
    .center { padding-top: calc(12vw + 30px); }
    .center p { margin-top: 16px; }
    .page .button { margin-top: 24px; }
    .cards { margin-top: 32px; }
    .glass .word { transform: translateY(calc(170px + 12vw)); }
  }'''

WIDE = re.compile(r'(<g class="wide-g" filter="url\(#reed\)">)(.*?)(</g>)')
NARROW = re.compile(r'(<g class="narrow" filter="url\(#reed-n\)">)(.*?)(</g>)')

# Without the two message bubbles the group is about 104px shorter, so the word moves down by half that.
NO_BUBBLES = '''
  /* No bubbles: the group is the word, the headline, the line and the button. */
  .cards { display: none; }
  @media (min-width: 1100px) {
    .glass .word { transform: translateY(calc(max(410px, 50vh) + 4.37vw - 84px - 1.05 * min(74px, 5.4vw))); }
  }'''

# Every page here has nothing behind the glass but the word: the light shapes are removed.
JOBS = [
    ('classic-calm-butter-word', 'classic-tight-butter-word', ''),
    ('classic-calm-butter-word', 'classic-tight-butter-word-quiet', NO_BUBBLES),
    ('classic-calm-beam', 'classic-tight-plain', ''),          # Beam's page, with the beam gone
    ('classic-calm-beam', 'classic-tight-plain-quiet', NO_BUBBLES),
]

for src, dst, extra in JOBS:
    base = (D / src / 'index.html').read_text(encoding='utf-8')
    html, n1 = WIDE.subn(lambda m: m.group(1) + m.group(3), base, 1)
    html, n2 = NARROW.subn(lambda m: m.group(1) + m.group(3), html, 1)
    assert n1 == n2 == 1
    html = html.replace('</style>', CSS + extra + '\n</style>', 1)
    assert html[html.index('</main>'):] == base[base.index('</main>'):]
    d = D / dst
    d.mkdir(exist_ok=True)
    (d / 'index.html').write_text(html, encoding='utf-8', newline='')
    print(dst)
