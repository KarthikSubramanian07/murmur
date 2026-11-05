"""The Royal wordmark hero (hero/royal.html), ported to Classic's blue and butter, as full pages.

Only the hero changes: <main> is replaced and its CSS is appended under `.st`; everything after
</main> stays byte-identical to classic-calm-messages. The word sits mid-screen, not at the top.

Usage: python -I wordmarks-stage.py <site/drafts dir>
"""
import re, sys, pathlib

D = pathlib.Path(sys.argv[1])
base = (D / 'classic-calm-messages' / 'index.html').read_text(encoding='utf-8')
MAIN = base[base.index('<main class="page">'):base.index('</main>') + len('</main>')]
grab = lambda pat: re.search(pat, MAIN, re.S).group(0)
NOTE = grab(r'<p class="agent-note".*?</p>')
H1 = grab(r'<h1>.*?</h1>')
P = re.search(r'<div class="center">.*?(<p>.*?</p>)', MAIN, re.S).group(1)
BTN = grab(r'<a class="button".*?</a>')
ASK = grab(r'<div class="card message c-ask">.*?</div>')
REPLY = grab(r'<div class="card message c-reply">.*?</div>')

BLUE, CREAM, BUTTER, INK = '#225BFF', '#FFFBEA', '#FDEE8C', '#0E1B45'


def ramp_filter(fid, scale):
    # A 12px sawtooth, tiled: each reed shows a squeezed slice of what is behind it. 12px matches
    # the reeds drawn over the rest of the page.
    return (f'<filter id="{fid}" x="0" y="0" width="100%" height="100%" color-interpolation-filters="sRGB">'
            '<feImage x="0" y="0" width="12" height="12" preserveAspectRatio="none" result="ramp" '
            'href="data:image/svg+xml,%3Csvg xmlns=\'http://www.w3.org/2000/svg\' width=\'12\' height=\'12\'%3E'
            '%3ClinearGradient id=\'g\'%3E%3Cstop offset=\'0\' stop-color=\'rgb(0,128,0)\'/%3E%3Cstop offset=\'1\' '
            'stop-color=\'rgb(255,128,0)\'/%3E%3C/linearGradient%3E%3Crect width=\'12\' height=\'12\' fill=\'url(%23g)\'/%3E%3C/svg%3E"/>'
            f'<feTile in="ramp" result="map"/><feDisplacementMap in="SourceGraphic" in2="map" scale="{scale}" '
            'xChannelSelector="R" yChannelSelector="G"/></filter>')


FILTERS = ('<svg width="0" height="0" style="position:absolute" aria-hidden="true" focusable="false">'
           + ramp_filter('st-reeds', 24) + ramp_filter('st-reeds-sm', 14) + '</svg>')

SMEAR = ('<defs><filter id="st-smear" x="-40%" y="-40%" width="180%" height="180%"><feGaussianBlur stdDeviation="52 8"/></filter>'
         '<filter id="st-soft" x="-40%" y="-40%" width="180%" height="180%"><feGaussianBlur stdDeviation="140 40"/></filter></defs>')


def light(haze, figure):
    return (f'<svg class="st-light" viewBox="0 0 1440 900" preserveAspectRatio="xMidYMid slice">{SMEAR}'
            f'<g filter="url(#st-soft)">{haze}</g><g filter="url(#st-smear)">{figure}</g></svg>')


# Royal's figure, recoloured: a butter figure smeared sideways, an ink-blue shadow beside it.
FIGURE = ('<g transform="translate(0 -110)">' +f'<ellipse cx="715" cy="230" rx="96" ry="78" fill="{BUTTER}"/>'
          f'<path d="M590 320 C660 295 800 295 870 330 L880 470 C860 530 770 550 650 545 C600 520 575 420 590 320Z" fill="{BUTTER}"/>'
          f'<rect x="400" y="240" width="340" height="40" rx="20" fill="{BUTTER}" opacity=".9"/>'
          f'<rect x="350" y="380" width="400" height="44" rx="22" fill="{BUTTER}" opacity=".7"/>'
          f'<rect x="440" y="500" width="320" height="36" rx="18" fill="#FFF6C4" opacity=".8"/>'
          f'<ellipse cx="910" cy="215" rx="88" ry="74" fill="{INK}"/>'
          f'<path d="M820 300 C880 280 1000 290 1050 330 L1090 640 C1070 780 960 850 870 830 C840 680 830 480 820 300Z" fill="#12309A" opacity=".95"/>'
          f'<rect x="900" y="410" width="320" height="40" rx="20" fill="#1640BF" opacity=".7"/>' + '</g>')
HAZE = f'<ellipse cx="690" cy="330" rx="300" ry="150" fill="{BUTTER}" opacity=".4"/>'

VARIANTS = {
    # The Royal layout on plain glass: nothing behind the word. Shown in the gallery's Tight group.
    'tight-royal': dict(light='', mark_top='40%', bubbles=False, cls=' st-big'),
    'tight-royal-butter': dict(light='', mark_top='40%', bubbles=False, cls=' st-big st-butter'),
    # The same, trimmed: a smaller word, the line on two lines, the agent note on one.
    'tight-royal-butter-trim': dict(light='', mark_top='42%', bubbles=False, cls=' st-big st-butter st-trim', inline=True),
    # The straight port: the figure behind the word, the copy at the foot of the screen.
    'stage': dict(light=light(HAZE, FIGURE), mark_top='45%', bubbles=False, cls=''),
    # The same, with the two messages under the button.
    'stage-messages': dict(light=light(HAZE, FIGURE), mark_top='38%', bubbles=True, cls=' st-compact'),
    # No figure: one long horizon of butter behind the lower half of the word, ink above.
    'dusk': dict(light=light(
        f'<ellipse cx="720" cy="400" rx="760" ry="110" fill="{BUTTER}" opacity=".55"/>'
        f'<ellipse cx="720" cy="40" rx="900" ry="260" fill="{INK}" opacity=".7"/>',
        f'<rect x="-60" y="392" width="1560" height="40" rx="20" fill="{BUTTER}"/>'
        f'<rect x="200" y="362" width="1040" height="20" rx="10" fill="#FFF6C4" opacity=".9"/>'
        f'<rect x="-60" y="455" width="1560" height="26" rx="13" fill="{BUTTER}" opacity=".35"/>'),
        mark_top='42%', bubbles=False, cls=''),
    # The copy sits over the middle of the word, the word dimmed so the headline leads.
    'overlap': dict(light=light(
        f'<ellipse cx="300" cy="640" rx="320" ry="160" fill="{BUTTER}" opacity=".45"/>',
        f'<ellipse cx="280" cy="650" rx="200" ry="70" fill="{BUTTER}"/>'
        f'<rect x="60" y="600" width="520" height="34" rx="17" fill="{BUTTER}" opacity=".7"/>'
        f'<ellipse cx="1120" cy="300" rx="200" ry="150" fill="{INK}" opacity=".8"/>'),
        mark_top='50%', bubbles=False, cls=' st-overlap'),
    # The name stacked, mur over mur, on the left; the copy on the right.
    'split': dict(light=light(
        f'<ellipse cx="420" cy="450" rx="320" ry="260" fill="{BUTTER}" opacity=".35"/>',
        f'<ellipse cx="380" cy="470" rx="150" ry="190" fill="{BUTTER}"/>'
        f'<rect x="120" y="360" width="460" height="40" rx="20" fill="{BUTTER}" opacity=".8"/>'
        f'<rect x="160" y="600" width="420" height="34" rx="17" fill="#FFF6C4" opacity=".7"/>'
        f'<ellipse cx="640" cy="420" rx="90" ry="220" fill="{INK}" opacity=".9"/>'),
        mark_top='50%', bubbles=False, cls=' st-split'),
}

CSS = f'''
  /* ---------- Hero: the Royal wordmark concept in Classic's colours (hero only) ---------- */
  /* The name and the light are one layer behind reeded glass, bent by the same 12px displacement;
     everything you read sits crisp on the near side. The name sits mid-screen. */
  .st {{ position: relative; min-height: max(820px, 100vh); min-height: max(820px, 100svh); overflow: clip; isolation: isolate;
    display: flex; flex-direction: column; align-items: center; padding: 0 24px 56px; text-align: center;
    background: linear-gradient(180deg, #2E64FF 0%, #2257F7 100%); }}
  .st .st-behind {{ position: absolute; inset: 0; z-index: 0; pointer-events: none; filter: url(#st-reeds); }}
  .st .st-light {{ position: absolute; inset: 0; width: 100%; height: 100%; }}
  .st .st-mark {{ position: absolute; left: 0; right: 0; top: var(--mark-top, 45%); transform: translateY(-50%);
    font: 700 min(24vw, 50vh)/.8 Inter, system-ui, sans-serif; letter-spacing: -.06em; text-indent: -.06em;
    white-space: nowrap; color: rgba(255, 251, 234, .88); filter: blur(2.5px); }}
  .st .st-mark span {{ display: inline-block; }}
  /* The glass: reed faces and grain in soft light, then the reed faces again, plain. */
  .st .st-glass {{ position: absolute; inset: 0; z-index: 1; pointer-events: none; background: var(--grain), var(--flutes);
    background-size: 180px 180px, auto; mix-blend-mode: soft-light; opacity: .75; }}
  .st::after {{ content: ""; position: absolute; inset: 0; z-index: 1; pointer-events: none; background: var(--flutes); opacity: .5; }}
  /* It fades into the blue of page 2 at the foot. */
  .st::before {{ content: ""; position: absolute; left: 0; right: 0; bottom: 0; height: 90px; z-index: 1; pointer-events: none;
    background: linear-gradient(180deg, transparent, {BLUE}); }}
  .st .agent-note {{ position: relative; z-index: 2; top: auto; margin: 0; padding-top: 18px; max-width: 640px; text-wrap: balance; }}
  .st .st-copy {{ position: relative; z-index: 2; margin-top: auto; }}
  .st h1 {{ font-size: clamp(34px, 4.3vw, 62px); text-shadow: 0 1px 2px rgba(14, 27, 69, .25), 0 4px 18px rgba(14, 27, 69, .3), 0 10px 60px rgba(14, 27, 69, .35); }}
  .st .st-copy p {{ max-width: 600px; margin: 22px auto 0; font-size: 18px; line-height: 1.55; text-wrap: balance;
    color: color-mix(in srgb, {CREAM} 92%, {BLUE}); text-shadow: 0 1px 16px rgba(14, 27, 69, .45); }}
  .st .button {{ margin-top: 30px; }}
  .st .st-bubbles {{ position: relative; z-index: 2; display: flex; gap: 20px; justify-content: center; margin-top: 34px; }}
  .st .st-bubbles .card {{ position: static; text-align: left; width: 300px; }}
  .st .st-bubbles .c-ask {{ --tilt: -1deg; }}
  .st .st-bubbles .c-reply {{ --tilt: 1deg; margin-top: 22px; }}

  /* Overlap: the copy centred over the word; the word dimmed so the headline leads. */
  .st-overlap {{ justify-content: flex-start; }}
  .st-overlap .st-copy {{ margin: auto 0; padding-top: 12vh; }}
  .st-overlap .st-mark {{ color: rgba(14, 27, 69, .42); filter: blur(3px); }}

  /* Split: mur over mur on the left, the copy on the right. */
  @media (min-width: 1000px) {{
    .st-split {{ display: grid; grid-template-columns: 1fr 1fr; grid-template-rows: auto 1fr; align-items: center; text-align: left; padding: 0 6vw 56px; }}
    .st-split .agent-note {{ grid-column: 1 / -1; justify-self: center; text-align: center; }}
    .st-split .st-copy {{ grid-column: 2; margin: 0; }}
    .st-split .st-copy p {{ margin-left: 0; }}
    .st-split h1 span {{ white-space: normal; }}
    .st-split .st-mark {{ right: 50%; text-align: center; font-size: min(22vw, 40vh); line-height: .74; }}
    .st-split .st-mark span {{ display: block; }}
  }}

  .st-compact h1 {{ font-size: clamp(32px, 3.6vw, 52px); }}
  .st-compact .st-mark {{ font-size: min(21vw, 44vh); }}
  .st-compact .st-copy p {{ margin-top: 16px; }}
  .st-compact .button {{ margin-top: 24px; }}
  .st-compact .st-bubbles {{ margin-top: 26px; }}
  /* Wider tracking than the others: at this size the refraction smears each edge by ~12px, and at
     -.06em the r's arm ran into the next m. */
  .st-big .st-mark {{ font-size: min(23.5vw, 50vh); letter-spacing: .01em; text-indent: .01em; }}
  .st-butter .st-mark {{ color: rgba(253, 238, 140, .92); }}
  .st-trim .st-mark {{ font-size: min(19vw, 42vh); }}
  /* Both headline lines in the same white, as Tyler asked. */
  .st-trim h1 span + span {{ color: var(--cream); }}
  /* The inline word: in the copy's flow but still under the glass. The copy stops being its own
     stacking context, so the word can sit under the reeds (z 0) while the text sits over them (z 2). */
  .st-trim .st-copy {{ z-index: auto; margin: auto 0; }}
  .st-trim .st-copy > :not(.st-mark) {{ position: relative; z-index: 2; }}
  .st .st-mark.st-inline {{ position: relative; z-index: 0; left: auto; right: auto; top: auto; transform: none;
    display: block; margin: 0 0 .12em; filter: blur(2.5px) url(#st-reeds); }}
  @media (min-width: 1000px) {{
    .st-trim .agent-note {{ max-width: none; white-space: nowrap; }}
    .st-trim .st-copy p {{ max-width: 740px; }}
  }}
  @media (max-width: 1100px) {{ .st h1 span {{ white-space: normal; text-wrap: balance; }} }}
  /* On a phone the name stacks, mur over mur, so it can stay big. */
  @media (max-width: 600px) {{
    .st {{ padding: 0 16px 40px; }}
    .st .st-behind {{ filter: url(#st-reeds-sm); }}
    .st .st-mark {{ --mark-top: 36% !important; font-size: 46vw; line-height: .72; }}
    .st .st-mark span {{ display: block; }}
    .st h1 {{ font-size: clamp(32px, 9.4vw, 42px); }}
    .st .st-copy p {{ font-size: 16.5px; margin-top: 18px; }}
    .st .button {{ margin-top: 24px; }}
    .st .st-bubbles {{ flex-direction: column; align-items: stretch; gap: 12px; }}
    .st .st-bubbles .card {{ width: 88%; }}
    .st .st-bubbles .c-reply {{ align-self: flex-end; margin-top: 0; }}
    .st-overlap .st-copy {{ padding-top: 30vh; }}
    .st-big .st-mark {{ font-size: 40vw; }}
    .st .st-mark.st-inline {{ filter: blur(2px) url(#st-reeds-sm); }}
  }}'''

for name, v in VARIANTS.items():
    bubbles = f'\n  <div class="st-bubbles">{ASK}{REPLY}</div>' if v['bubbles'] else ''
    mark = '<div class="st-mark"><span>mur</span><span>mur</span></div>'
    # inline: the word sits in the copy's flow, a fixed gap above the headline, instead of being
    # pinned to a height of the screen, and the group is centred, so the gap holds at any size.
    if v.get('inline'):
        behind_mark, copy_mark = '', '\n    ' + mark.replace('class="st-mark"', 'class="st-mark st-inline" aria-hidden="true"')
    else:
        behind_mark, copy_mark = mark, ''
    hero = (f'<main class="st{v["cls"]}" style="--mark-top: {v["mark_top"]}">\n  {FILTERS}\n'
            f'  <div class="st-behind" aria-hidden="true">{v["light"]}{behind_mark}</div>\n'
            f'  <div class="st-glass" aria-hidden="true"></div>\n  {NOTE}\n'
            f'  <div class="st-copy">{copy_mark}\n    {H1}\n    {P}\n    {BTN}\n  </div>{bubbles}\n</main>')
    html = base.replace(MAIN, hero, 1).replace('</style>', CSS + '\n</style>', 1)
    assert html.index('</main>') and html[html.index('</main>'):] == base[base.index('</main>'):]
    d = D / f'classic-{name}'
    d.mkdir(exist_ok=True)
    (d / 'index.html').write_text(html, encoding='utf-8', newline='')
    print(d.name)
