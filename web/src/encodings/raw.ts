import type { Framebuffer } from '../framebuffer';
import type { RectHeader } from '../types';

/**
 * Decode Raw encoding: pixel data is uncompressed RGBA (after SetPixelFormat).
 */
export function decodeRaw(
  fb: Framebuffer,
  header: RectHeader,
  data: DataView,
): void {
  const { x, y, width, height } = header;
  const numPixels = width * height;
  const rgbaData = new Uint8Array(data.buffer, data.byteOffset, numPixels * 4);
  fb.writeRect(x, y, width, height, rgbaData);
}
