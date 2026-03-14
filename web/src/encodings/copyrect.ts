import type { Framebuffer } from '../framebuffer';
import type { RectHeader } from '../types';

/**
 * Decode CopyRect encoding: copy pixels from another region of the framebuffer.
 */
export function decodeCopyRect(
  fb: Framebuffer,
  header: RectHeader,
  data: DataView,
): void {
  const srcX = data.getUint16(0);
  const srcY = data.getUint16(2);
  fb.copyRect(srcX, srcY, header.x, header.y, header.width, header.height);
}
