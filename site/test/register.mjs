// Lets node import the Worker as Wrangler bundles it: a .md or .html import is its text (the Text
// rule in wrangler.jsonc). `node --test --import ./site/test/register.mjs site/test/*.test.mjs`
import { register } from 'node:module';

register('./text-loader.mjs', import.meta.url);
