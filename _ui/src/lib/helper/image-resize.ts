/**
 * Shrinks large images before they are attached, like OpenCode's
 * media.image settings: providers reject or bill heavily for very large
 * images, and a 12-megapixel phone photo carries no detail a model uses.
 *
 * PNG, JPEG and WebP above IMAGE_MAX_EDGE pixels or IMAGE_MAX_BYTES are
 * redrawn on a canvas at the largest size that fits, keeping the aspect
 * ratio, and encoded as JPEG (PNG when the image has transparency), trying
 * lower qualities and smaller sizes until it fits. GIF is left alone because
 * a canvas would drop its animation. When the browser cannot decode the
 * image, the original is kept.
 */
export const IMAGE_MAX_EDGE = 2000;
export const IMAGE_MAX_BYTES = 3.75 * 1024 * 1024; // ~5 MiB once base64 encoded

const resizable = new Set(['image/png', 'image/jpeg', 'image/webp']);

/** The size an image should be drawn at, or null when it already fits. */
export function targetImageSize(width: number, height: number, bytes: number, maxEdge = IMAGE_MAX_EDGE, maxBytes = IMAGE_MAX_BYTES): { width: number; height: number } | null {
  if (width <= 0 || height <= 0) return null;
  const edge = Math.max(width, height);
  if (edge <= maxEdge && bytes <= maxBytes) return null;
  const scale = Math.min(1, maxEdge / edge);
  return { width: Math.max(1, Math.round(width * scale)), height: Math.max(1, Math.round(height * scale)) };
}

export interface ResizedImage {
  blob: Blob;
  type: string;
  name: string;
  width: number;
  height: number;
  resized: boolean;
}

/** Returns the image to attach: the original, or a smaller re-encoding. */
export async function shrinkImage(file: File): Promise<ResizedImage | null> {
  if (!resizable.has(file.type) || typeof createImageBitmap !== 'function' || typeof OffscreenCanvas !== 'function' && typeof document === 'undefined') return null;
  let bitmap: ImageBitmap;
  try {
    bitmap = await createImageBitmap(file);
  } catch {
    return null;
  }
  try {
    let target = targetImageSize(bitmap.width, bitmap.height, file.size);
    if (!target) return { blob: file, type: file.type, name: file.name, width: bitmap.width, height: bitmap.height, resized: false };
    const draw = (w: number, h: number) => {
      const canvas: OffscreenCanvas | HTMLCanvasElement = typeof OffscreenCanvas === 'function' ? new OffscreenCanvas(w, h) : Object.assign(document.createElement('canvas'), { width: w, height: h });
      const ctx = canvas.getContext('2d') as CanvasRenderingContext2D | OffscreenCanvasRenderingContext2D | null;
      if (!ctx) throw new Error('canvas unavailable');
      ctx.drawImage(bitmap, 0, 0, w, h);
      return { canvas, ctx };
    };
    const encode = async (canvas: OffscreenCanvas | HTMLCanvasElement, type: string, quality?: number): Promise<Blob> => {
      if ('convertToBlob' in canvas) return canvas.convertToBlob({ type, quality });
      return new Promise((resolve, reject) => (canvas as HTMLCanvasElement).toBlob(b => (b ? resolve(b) : reject(new Error('encode failed'))), type, quality));
    };
    for (let attempt = 0; attempt < 6; attempt++) {
      const { canvas, ctx } = draw(target.width, target.height);
      const transparent = file.type !== 'image/jpeg' && hasTransparency(ctx, target.width, target.height);
      const type = transparent ? 'image/png' : 'image/jpeg';
      for (const quality of transparent ? [undefined] : [0.85, 0.7, 0.55]) {
        const blob = await encode(canvas, type, quality);
        if (blob.size <= IMAGE_MAX_BYTES) {
          const ext = type === 'image/png' ? '.png' : '.jpg';
          return { blob, type, name: file.name.replace(/\.[^.]+$/, '') + ext, width: target.width, height: target.height, resized: true };
        }
      }
      target = { width: Math.max(1, Math.round(target.width * 0.75)), height: Math.max(1, Math.round(target.height * 0.75)) };
    }
    return null;
  } catch {
    return null;
  } finally {
    bitmap.close();
  }
}

function hasTransparency(ctx: CanvasRenderingContext2D | OffscreenCanvasRenderingContext2D, width: number, height: number): boolean {
  // Sample a grid instead of every pixel; a cut-out shows up in any sample.
  const step = Math.max(1, Math.floor(Math.min(width, height) / 64));
  try {
    const data = ctx.getImageData(0, 0, width, height).data;
    for (let y = 0; y < height; y += step) {
      for (let x = 0; x < width; x += step) {
        if (data[(y * width + x) * 4 + 3] < 255) return true;
      }
    }
  } catch {
    return true;
  }
  return false;
}

/** File to attach: the shrunk image when resizing helped, else the original. */
export async function attachableFile(file: File): Promise<File> {
  const shrunk = await shrinkImage(file);
  if (!shrunk || !shrunk.resized) return file;
  return new File([shrunk.blob], shrunk.name, { type: shrunk.type });
}
