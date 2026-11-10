// The load hook register.mjs installs: a .md or .html module is a default export of its text.
import { readFile } from 'node:fs/promises';

export async function load(url, context, next) {
  if (url.endsWith('.md') || url.endsWith('.html')) {
    const text = await readFile(new URL(url), 'utf8');
    return { format: 'module', shortCircuit: true, source: `export default ${JSON.stringify(text)};` };
  }
  return next(url, context);
}
