/**
 * Deterministic image fixtures generated at runtime into .build/ (nothing
 * binary is committed). The backend validates uploads by magic bytes and
 * decodes the image, so the PNGs here are real, decodable files:
 *
 *   - tiny.png    1x1 grayscale, a few dozen bytes
 *   - oversize.png 1100x1100 incompressible RGB noise, > 2MB after deflate
 *   - invalid.gif GIF89a header — wrong magic, rejected by type sniffing
 */

import { deflateSync } from 'node:zlib';
import { mkdirSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const fixturesDir = path.join(here, '.build', 'image-fixtures');

// Node 24 exposes zlib.crc32; a tiny local fallback keeps older tooling alive.
import { crc32 } from 'node:zlib';
function crc32safe(body) {
  return typeof crc32 === 'function' ? crc32(body) : fallbackCrc32(body);
}
function fallbackCrc32(buf) {
  let c;
  const table = [];
  for (let n = 0; n < 256; n += 1) {
    c = n;
    for (let k = 0; k < 8; k += 1) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    table[n] = c >>> 0;
  }
  c = 0xffffffff;
  for (const byte of buf) c = table[(c ^ byte) & 0xff] ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}

function pngChunk(type, data) {
  const length = Buffer.alloc(4);
  length.writeUInt32BE(data.length, 0);
  const body = Buffer.concat([Buffer.from(type, 'ascii'), data]);
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32safe(body), 0);
  return Buffer.concat([length, body, crc]);
}

function buildPng(width, height, pixelBytes) {
  const signature = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(width, 0);
  ihdr.writeUInt32BE(height, 4);
  ihdr[8] = 8; // bit depth
  ihdr[9] = 2; // color type: truecolor RGB
  // compression 0, filter 0, interlace 0 already zero
  const raw = Buffer.alloc((width * 3 + 1) * height);
  let offset = 0;
  for (let y = 0; y < height; y += 1) {
    raw[offset] = 0; // filter: none
    offset += 1;
    pixelBytes.copy(raw, offset, y * width * 3, (y + 1) * width * 3);
    offset += width * 3;
  }
  return Buffer.concat([
    signature,
    pngChunk('IHDR', ihdr),
    pngChunk('IDAT', deflateSync(raw, { level: 0 })), // level 0: keep noise incompressible
    pngChunk('IEND', Buffer.alloc(0)),
  ]);
}

let seeded = 0x2f6e2b1;
function noiseByte() {
  // xorshift keeps generation deterministic without a dependency.
  seeded ^= seeded << 13; seeded >>>= 0;
  seeded ^= seeded >>> 17;
  seeded ^= seeded << 5; seeded >>>= 0;
  return seeded & 0xff;
}

export function ensureImageFixtures() {
  mkdirSync(fixturesDir, { recursive: true });
  const tiny = path.join(fixturesDir, 'tiny.png');
  const oversize = path.join(fixturesDir, 'oversize.png');
  const invalid = path.join(fixturesDir, 'invalid.gif');
  const hugePixels = path.join(fixturesDir, 'huge-pixels.png');

  const gray = Buffer.alloc(3, 0x80);
  writeFileSync(tiny, buildPng(1, 1, gray));

  const side = 1100;
  const noise = Buffer.alloc(side * side * 3);
  for (let i = 0; i < noise.length; i += 1) noise[i] = noiseByte();
  writeFileSync(oversize, buildPng(side, side, noise));

  writeFileSync(invalid, Buffer.concat([
    Buffer.from('GIF89a', 'ascii'),
    Buffer.alloc(64, 0x00),
  ]));

  // 30MP solid image compresses to a few hundred KB: under the 2MB size
  // limit but over the backend's 25M-pixel cap, so it only passes when the
  // browser pipeline clamps resolution too (not just file size).
  writeFileSync(hugePixels, buildPng(6000, 5000, Buffer.alloc(6000 * 5000 * 3, 0x3c)));

  return { tiny, oversize, invalid, hugePixels };
}
