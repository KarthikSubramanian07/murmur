# Brand

The brand dates from 2026-11-10. Its source is Tyler's Figma file, "murmur"; the files here were
cut from that file's SVG export, so the shapes are the designer's and nothing was redrawn.

## The mark

Two agents side by side that read as an M: two agents in one repo, talking. The left one has round
eyes; the right one's are slanted like a quotation mark.

| File | What it is |
| --- | --- |
| `docs/assets/brand/mark.svg` | The mark, filled, ink |
| `docs/assets/brand/mark-outline.svg` | The mark, outlined, ink |
| `docs/assets/brand/wordmark.svg` | "murmur" in Fredoka One, as outlines, ink |
| `docs/assets/brand/lockup.svg` | The mark as the M of "murmur", outlined, ink, for light grounds |
| `docs/assets/brand/lockup-on-blue.svg` | The same, filled: cream mark, butter letters |
| `docs/assets/brand/lockup-stacked-on-blue.svg` | The mark over the wordmark, cream and butter |
| `docs/assets/brand/icon.svg` | The cream mark on a blue tile: the tab icon and app icon |
| `docs/assets/banner.svg` | The README's header |

`site/favicon.svg` and `internal/web/static/assets/favicon.svg` are copies of `icon.svg`, because
the site and the binary each serve their own files. `site/favicon-32.png` and
`site/apple-touch-icon.png` are rasters of it; the touch icon has square corners, since iOS rounds
them.

The Figma file's rules: leave one eye-width of clear space on every side, keep the mark at least
24px wide and the inline lockup at least 96px wide, and never redraw or stretch either. Another
color is a change of `fill`, not a new drawing.

## Color

The site's four colors, which the logo files use too:

| Name | Hex | Use |
| --- | --- | --- |
| blue | `#225BFF` | Page grounds, the icon's tile, links on cream |
| cream | `#FFFBEA` | The sheets, text on blue, the mark on blue |
| butter | `#FDEE8C` | The one loud accent: the hero word, the primary button, message cards |
| ink | `#0E1B45` | Text, and the logo on light grounds |

The Figma file proposes a different palette: a lighter blue `#258EE3`, cream `#FFFDF4`, butter
`#FFEF8C` and navy `#05224D`. The site was built in it on 2026-11-10 and Tyler preferred the look
it already had, so the palette above stayed and the logo files were colored to match. Two numbers
from that trial: cream on `#258EE3` is 3.4:1, below WCAG AA for body text, where cream on
`#225BFF` is 5.1:1.

## Type

The wordmark is Fredoka One and ships as outlines, so nothing loads the font. The site is set in
Inter, and its hero word is "murmur" in Inter behind the reeded glass, not the wordmark. The Figma
file proposes Rubik for text and JetBrains Mono for commands; neither was adopted.

## Where the brand is not applied

The `mur ui` page keeps its own look: it is drawn as a sibling of T3 Code, with T3 Code's tokens
(`internal/web/static/assets/style.css`). It takes the icon only.

## Renders

`site/how-wide.mp4`, `site/how-tall.mp4` and their posters are rendered from
`site/src/how-scene.html` by `site/src/render-video.mjs`. `site/og.jpg` and
`docs/assets/murmur-demo.gif` come from the wide film: the card is its last frame, and the GIF is
the film at about 14 frames a second, 960px wide. A change to the palette means rendering all of
them again.
