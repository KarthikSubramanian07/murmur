// Renders the scene to video, from site/: node src/render-video.mjs src/how-scene.html . [wide|tall]
// Needs playwright-core importable, Chromium at the path below, and ffmpeg on PATH. The posters are
// the first and last frames it writes as PNG, converted to .webp.
// paint(t) is pure, so each frame is the page at an exact time and the clock stays true.
import { chromium } from 'playwright-core';
import { spawn } from 'node:child_process';
import path from 'node:path';
import fs from 'node:fs';
import { pathToFileURL } from 'node:url';

const [source, outDir, only] = process.argv.slice(2);
// The scroll page's pacing, played over RUN seconds: motion gets time, waiting passes quickly.
const FPS = 60, RUN = 16, HOLD = 3;
const FORMATS = {
  wide: { viewport: { width: 1600, height: 980 }, scale: 1.2, crf: 20 },
  tall: { viewport: { width: 392, height: 800 }, scale: 2.5, crf: 22 },
};
const chrome = path.join(process.env.LOCALAPPDATA, 'ms-playwright', 'chromium-1248', 'chrome-win64', 'chrome.exe');
fs.mkdirSync(outDir, { recursive: true });

// A copy of the page that exposes paint and shows only the stage, not committed anywhere.
const html = fs.readFileSync(source, 'utf8')
  .replace('let queued = false;', 'window.__paint = paint; window.__tAt = tAt; window.__setup = () => { choosePaths(); }; let queued = false;')
  .replace('</style>', `  #how > .route, #setup, main, header, nav, footer { display: none !important; }
  #how::after { display: none !important; }
  html, body { margin: 0; overflow: hidden; }
  .stage { position: relative !important; min-height: 0 !important; }
</style>`);
if (!html.includes('window.__paint')) throw new Error('paint hook not injected');
const temp = path.join(path.dirname(source), `.render-${process.pid}.html`);
fs.writeFileSync(temp, html);

const br = await chromium.launch({ executablePath: chrome });
try {
  for (const [name, f] of Object.entries(FORMATS)) {
    if (only && only !== name) continue;
    const ctx = await br.newContext({ viewport: f.viewport, deviceScaleFactor: f.scale });
    const page = await ctx.newPage();
    await page.goto(pathToFileURL(temp).href);
    await page.evaluate(() => document.fonts.ready);
    await page.evaluate(() => window.__setup());
    const out = path.join(outDir, `how-${name}.mp4`);
    const ff = spawn('ffmpeg', ['-y', '-loglevel', 'error', '-f', 'image2pipe', '-c:v', 'png', '-framerate', String(FPS), '-i', '-',
      '-vf', 'scale=in_range=pc:out_range=tv:out_color_matrix=bt709,format=yuv420p',
      '-c:v', 'libx264', '-preset', 'slow', '-crf', String(f.crf), '-tune', 'animation',
      '-colorspace', 'bt709', '-color_primaries', 'bt709', '-color_trc', 'bt709', '-color_range', 'tv',
      '-movflags', '+faststart', '-an', out], { stdio: ['pipe', 'inherit', 'inherit'] });
    const done = new Promise((ok, no) => ff.on('close', c => c ? no(new Error(`ffmpeg ${c}`)) : ok()));
    const frames = Math.round((RUN + HOLD) * FPS);
    let png;
    for (let i = 0; i < frames; i++) {
      const p = i / FPS / RUN;
      // The hold repeats the settled frame; there is nothing new to draw.
      if (p <= 1 || !png) {
        await page.evaluate(v => window.__paint(window.__tAt(v)), p);
        png = await page.screenshot({ type: 'png' });
        if (i === 0) fs.writeFileSync(path.join(outDir, `how-${name}-first.png`), png);
      }
      if (!ff.stdin.write(png)) await new Promise(r => ff.stdin.once('drain', r));
      if (i % 120 === 0) console.log(name, `${i}/${frames}`);
    }
    fs.writeFileSync(path.join(outDir, `how-${name}-last.png`), png);
    ff.stdin.end();
    await done;
    console.log(out, (fs.statSync(out).size / 1e6).toFixed(2), 'MB');
    await ctx.close();
  }
} finally {
  await br.close();
  fs.rmSync(temp, { force: true });
}
