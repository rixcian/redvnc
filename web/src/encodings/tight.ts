import type { Framebuffer } from '../framebuffer';
import type { RectHeader } from '../types';
import { readCompactLength } from '../rfb-parser';

/**
 * Decode Tight encoding. Handles solid fill, JPEG, and basic (zlib) sub-types.
 * Tight processes the rectangle as 64x64 tiles.
 */
export class TightDecoder {
  private pako: typeof import('pako') | null = null;

  async init(): Promise<void> {
    this.pako = await import('pako');
  }

  async decode(fb: Framebuffer, header: RectHeader, data: DataView): Promise<void> {
    const { x, y, width, height } = header;
    let offset = 0;

    for (let ty = 0; ty < height; ty += 64) {
      const tileH = Math.min(64, height - ty);
      for (let tx = 0; tx < width; tx += 64) {
        const tileW = Math.min(64, width - tx);
        const control = data.getUint8(offset);
        offset += 1;

        const subType = control & 0x0f;

        if (subType === 0x08) {
          // Solid fill: 3 bytes RGB (Tight always uses 3-byte pixels for 32bpp truecolor)
          const r = data.getUint8(offset);
          const g = data.getUint8(offset + 1);
          const b = data.getUint8(offset + 2);
          offset += 3;

          const rgbaData = new Uint8Array(tileW * tileH * 4);
          for (let i = 0; i < tileW * tileH; i++) {
            rgbaData[i * 4] = r;
            rgbaData[i * 4 + 1] = g;
            rgbaData[i * 4 + 2] = b;
            rgbaData[i * 4 + 3] = 255;
          }
          fb.writeRect(x + tx, y + ty, tileW, tileH, rgbaData);
        } else if (subType === 0x09) {
          // JPEG
          const { length, bytesRead } = readCompactLength(data, offset);
          offset += bytesRead;

          const jpegData = new Uint8Array(data.buffer, data.byteOffset + offset, length);
          offset += length;

          const blob = new Blob([jpegData.slice()], { type: 'image/jpeg' });
          const bitmap = await createImageBitmap(blob);
          fb.drawBitmap(bitmap, x + tx, y + ty, tileW, tileH);
          bitmap.close();
        } else {
          // Basic (zlib compressed)
          const { length, bytesRead } = readCompactLength(data, offset);
          offset += bytesRead;

          const compressed = new Uint8Array(data.buffer, data.byteOffset + offset, length);
          offset += length;

          if (this.pako) {
            const decompressed = this.pako.inflate(compressed);
            // Tight basic uses 3-byte RGB pixels
            const rgbaData = new Uint8Array(tileW * tileH * 4);
            for (let i = 0; i < tileW * tileH; i++) {
              rgbaData[i * 4] = decompressed[i * 3];
              rgbaData[i * 4 + 1] = decompressed[i * 3 + 1];
              rgbaData[i * 4 + 2] = decompressed[i * 3 + 2];
              rgbaData[i * 4 + 3] = 255;
            }
            fb.writeRect(x + tx, y + ty, tileW, tileH, rgbaData);
          }
        }
      }
    }
  }
}
