/**
 * post-build patch: Umi 4 SPA build does not emit `<html lang="...">`
 * (htmlPageOpts.lang is ignored by the SSR template), and Lighthouse
 * `html-has-lang` fails without it. We rewrite the generated index.html
 * to add lang="zh-CN" before serving, and drop the legacy
 * `X-UA-Compatible` meta (IE has been dead for years; the meta is not
 * required by any current browser). Idempotent: re-running the patch
 * on an already-patched file is a no-op.
 */
import fs from 'node:fs';
import path from 'node:path';

const target = path.resolve(process.cwd(), 'dist/index.html');
let html = fs.readFileSync(target, 'utf8');

let changed = 0;
if (!/<html [^>]*\blang=/i.test(html)) {
  html = html.replace(/<html>/i, '<html lang="zh-CN">');
  changed++;
}
if (/<meta\s+http-equiv=["']?X-UA-Compatible/i.test(html)) {
  html = html.replace(
    /\s*<meta\s+http-equiv=["']?X-UA-Compatible[^>]*>\n?/i,
    '',
  );
  changed++;
}

if (changed > 0) {
  fs.writeFileSync(target, html);
  console.log(`patched ${path.basename(target)} (${changed} change(s))`);
} else {
  console.log(`${path.basename(target)} already patched, no-op`);
}