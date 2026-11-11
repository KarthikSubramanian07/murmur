# Brand

The brand dates from 2026-11-10. Its source is Tyler's Figma file, "murmur"; the files here were
cut from that file's SVG export, so the shapes are the designer's and nothing was redrawn.

## The mark

Two agents side by side that read as an M: two agents in one repo, talking. The left one has round
eyes; the right one's are slanted like a quotation mark.

| File | What it is |
| --- | --- |
| `docs/assets/brand/mark.svg` | The mark, filled, navy |
| `docs/assets/brand/mark-outline.svg` | The mark, outlined, navy |
| `docs/assets/brand/wordmark.svg` | "murmur" in Fredoka One, as outlines, navy |
| `docs/assets/brand/lockup.svg` | The mark as the M of "murmur", outlined, navy, for light grounds |
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

| Name | Hex | Use |
| --- | --- | --- |
| cream/100 | `#FFFDF4` | Page ground, text on blue and navy |
| cream/200 | `#FBF5DE` | Surfaces on cream |
| cream/300 | `#EFE7C7` | Borders and rules |
| butter/500 | `#FFEF8C` | The one loud accent |
| blue/300 | `#9CCBF3` | Quiet labels on navy |
| blue/500 | `#258EE3` | The brand blue: tiles and page grounds |
| blue/700 | `#1767B0` | Blue as text, and under cream text, on cream |
| navy/500 | `#3A4F73` | Secondary text |
| navy/900 | `#05224D` | Ink |

Contrast, measured 2026-11-10, decides which blue goes where:

- Cream on blue/500 is 3.4:1 and butter on it is 3.0:1. That passes WCAG AA for large text and
  fails it for body text (4.5:1). The site still sets body text on blue/500, because the brand
  blue is the ground the design asks for; the earlier blue, `#225BFF`, gave 5.1:1.
- Cream on blue/700 is 5.7:1. Links, step numbers and buttons on the cream sheets use it
  (`--blue-ink` in the site's CSS).
- Navy on blue/500 is 4.5:1, and navy on butter is 13.4:1.

## Type

Fredoka One is the wordmark only, and ships as outlines, so nothing loads it. Rubik carries
everything people read on the site, and JetBrains Mono carries commands; the site loads both from
Google Fonts.

## Where the brand is not applied

The `mur ui` page keeps its own look: it is drawn as a sibling of T3 Code, with T3 Code's tokens
and Inter (`internal/web/static/assets/style.css`). It takes the icon only.

## Renders

`site/how-wide.mp4`, `site/how-tall.mp4` and their posters are rendered from
`site/src/how-scene.html` by `site/src/render-video.mjs`. `site/og.jpg` and
`docs/assets/murmur-demo.gif` come from the wide film: the card is its last frame, and the GIF is
the film at about 14 frames a second, 960px wide. A change to the palette means rendering all of
them again.
