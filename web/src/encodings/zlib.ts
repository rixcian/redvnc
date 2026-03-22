import { Inflate } from 'fflate';
import type { Framebuffer } from '../framebuffer';
import type { RectHeader } from '../types';

/**
 * Persistent zlib decompressor. The RFB spec requires the zlib stream to persist
 * across multiple rectangles within a connection.
 */
export class ZlibDecoder {
  private inflater: Inflate | null = null;
  private chunks: Uint8Array[] = [];
  private firstPush = true;

  async init(): Promise<void> {
    this.inflater = new Inflate((chunk) => {
      this.chunks.push(chunk.slice());
    });
  }

  decode(fb: Framebuffer, header: RectHeader, data: DataView): void {
    if (!this.inflater) {
      throw new Error('ZlibDecoder not initialized. Call init() first.');
    }

    const { x, y, width, height } = header;
    const compressedLen = data.getUint32(0);
    let compressed = new Uint8Array(data.buffer, data.byteOffset + 4, compressedLen);

    // Skip the 2-byte zlib header on the first push
    if (this.firstPush) {
      compressed = compressed.subarray(2);
      this.firstPush = false;
    }

    this.chunks.length = 0;
    this.inflater.push(compressed, false);

    let decompressed: Uint8Array;
    if (this.chunks.length === 1) {
      decompressed = this.chunks[0];
    } else {
      const total = this.chunks.reduce((s, c) => s + c.length, 0);
      decompressed = new Uint8Array(total);
      let off = 0;
      for (const c of this.chunks) {
        decompressed.set(c, off);
        off += c.length;
      }
    }

    fb.writeRect(x, y, width, height, decompressed);
  }
}
