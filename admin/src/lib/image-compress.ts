/**
 * Client-side image compression for the product gallery upload.
 *
 * The backend hard-rejects uploads over 2MB, so oversized picks are re-encoded
 * in the browser before the POST: decode via createImageBitmap, draw to a
 * canvas at a (possibly reduced) size, encode with canvas.toBlob. The ladder
 * tries quality steps at the original resolution first, then walks the pixel
 * size down — the final 640px JPEG@0.5 step is small enough that any input
 * lands under the limit, satisfying the "always fits" guarantee.
 *
 * Format strategy: WebP first (keeps alpha, best ratio, accepted by the
 * backend). Some browsers (Safari) silently fall back to PNG on unsupported
 * toBlob types, so the produced blob type is verified; the JPEG fallback
 * flattens transparency onto white.
 */

/** Backend upload limit minus a safety margin for container overhead. */
export const COMPRESS_TARGET_BYTES = 2 * 1024 * 1024 - 128 * 1024;

/**
 * Backend IMAGE_MAX_PIXELS: uploads with more decoded pixels are rejected
 * outright (422), so the ladder must clamp resolution below it too — a
 * size-fit at 27MP would still fail server-side.
 */
export const MAX_IMAGE_PIXELS = 25_000_000;

/** Never encode below this pixel width/height — thumbnails stay legible. */
const MIN_DIMENSION = 640;

/** Quality ladder per resolution step, tried in order. */
const QUALITY_STEPS = [0.85, 0.7, 0.55] as const;

/** Resolution scale steps applied when quality alone is not enough. */
const SCALE_STEPS = [1, 0.75, 0.5, 0.35] as const;

export interface CompressionStep {
  scale: number;
  quality: number;
  type: 'image/webp' | 'image/jpeg';
}

/**
 * Pure strategy: the ordered (scale, quality) attempts for a given file.
 * Exported for unit tests; the browser encoder consumes it in order.
 */
export function compressionPlan(preferWebP: boolean): CompressionStep[] {
  const type: CompressionStep['type'] = preferWebP ? 'image/webp' : 'image/jpeg';
  const plan: CompressionStep[] = [];
  for (const scale of SCALE_STEPS) {
    for (const quality of QUALITY_STEPS) {
      plan.push({ scale, quality, type });
    }
  }
  return plan;
}

export interface CompressResult {
  file: File;
  /** False when the input was already within the limit (untouched pass-through). */
  compressed: boolean;
  /** Size before (and after when compressed) in bytes, for the UX message. */
  originalBytes: number;
}

function canvasToBlob(
  canvas: HTMLCanvasElement,
  type: string,
  quality: number,
): Promise<Blob | null> {
  return new Promise((resolve) => {
    canvas.toBlob((blob) => resolve(blob), type, quality);
  });
}

async function encode(
  bitmap: ImageBitmap,
  width: number,
  height: number,
  type: string,
  quality: number,
): Promise<Blob | null> {
  const canvas = document.createElement('canvas');
  canvas.width = Math.max(1, Math.round(width));
  canvas.height = Math.max(1, Math.round(height));
  const ctx = canvas.getContext('2d');
  if (!ctx) return null;
  if (type === 'image/jpeg') {
    // JPEG has no alpha channel; flatten onto white instead of black.
    ctx.fillStyle = '#ffffff';
    ctx.fillRect(0, 0, canvas.width, canvas.height);
  }
  ctx.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
  const blob = await canvasToBlob(canvas, type, quality);
  if (blob && blob.type !== type) {
    // Browser silently downgraded the requested type (e.g. Safari webp→png):
    // the PNG of a noisy photo can exceed the limit, so treat as failure and
    // let the caller fall through to the JPEG steps.
    return null;
  }
  return blob;
}

function rename(original: string, extension: string): string {
  const base = original.replace(/\.[^.]+$/, '');
  return `${base}.${extension}`;
}

/**
 * Returns a file at or under `limit` bytes whose pixel count also stays below
 * `maxPixels` (the backend rejects oversized bitmaps independently of file
 * size). Inputs already within both limits pass through byte-identical (no
 * re-encode, no generation loss).
 */
export async function compressImage(
  file: File,
  limit: number = COMPRESS_TARGET_BYTES,
  maxPixels: number = MAX_IMAGE_PIXELS,
): Promise<CompressResult> {
  const bitmap = await createImageBitmap(file);
  try {
    // Pass through only when BOTH backend limits are already satisfied; a
    // small-but-huge-bitmap file would sail past the size check and still
    // draw a 422 on the pixel limit.
    if (file.size <= limit && bitmap.width * bitmap.height <= maxPixels) {
      return { file, compressed: false, originalBytes: file.size };
    }
    // Clamp every ladder step so width*scale x height*scale <= maxPixels.
    let pixelScale = Math.sqrt(maxPixels / (bitmap.width * bitmap.height));
    if (pixelScale < 1) {
      // Round-up guard: rounding each side up can push the product past the
      // limit even at the exact sqrt scale (e.g. 7000x5000 lands at
      // 5916x4226 = 25,001,016 > 25M). Shrink until the rounded dims fit.
      while (
        Math.round(bitmap.width * pixelScale) * Math.round(bitmap.height * pixelScale) >
        maxPixels
      ) {
        pixelScale *= 0.995;
      }
    }
    const clamp = (scale: number): number =>
      pixelScale >= 1 ? scale : Math.min(scale, pixelScale);
    const side = Math.max(bitmap.width, bitmap.height);
    for (const step of compressionPlan(true)) {
      const scale = clamp(step.scale);
      const target = Math.round(side * scale);
      if (target < MIN_DIMENSION && scale !== clamp(SCALE_STEPS[SCALE_STEPS.length - 1])) {
        continue;
      }
      const blob = await encode(bitmap, bitmap.width * scale, bitmap.height * scale, step.type, step.quality);
      if (blob && blob.size <= limit) {
        const extension = step.type === 'image/webp' ? 'webp' : 'jpg';
        const renamed = rename(file.name, extension);
        return {
          file: new File([blob], renamed, { type: step.type }),
          compressed: true,
          originalBytes: file.size,
        };
      }
    }
    // Guaranteed floor: tiny JPEG. Any decoded image at 640px q0.5 is far
    // below 2MB; if even this fails the source is not a decodable image and
    // the backend would reject it anyway.
    const floorScale = Math.min(1, pixelScale);
    const blob = await encode(
      bitmap,
      MIN_DIMENSION * floorScale,
      MIN_DIMENSION * floorScale,
      'image/jpeg',
      0.5,
    );
    if (blob && blob.size <= limit) {
      return {
        file: new File([blob], rename(file.name, 'jpg'), { type: 'image/jpeg' }),
        compressed: true,
        originalBytes: file.size,
      };
    }
    throw new Error('compress: unable to fit the image under the size limit');
  } finally {
    bitmap.close();
  }
}
