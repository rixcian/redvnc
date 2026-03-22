import type { Framebuffer } from '../framebuffer';
import type { RectHeader } from '../types';
import { readCompactLength } from '../rfb-parser';

/**
 * Decode Tight encoding. Handles solid fill, JPEG, and basic (zlib) sub-types.
 * Tight uses 4 persistent zlib streams (selected by control byte bits 0-1).
 * The zlib dictionary is preserved across tiles and rectangles within a
 * connection, as required by the Tight encoding specification.
 */
export class TightDecoder {
  private pako: typeof import('pako') | null = null;
  private streams: (import('pako').Inflate | null)[] = [null, null, null, null];

  async init(): Promise<void> {
    this.pako = await import('pako');
    for (let i = 0; i < 4; i++) {
      this.streams[i] = new this.pako.Inflate();
    }
  }

  /**
   * Decompress data using a persistent zlib stream.
   *
   * pako 2.x only calls onData when the internal output buffer is completely
   * full (avail_out === 0) or on Z_STREAM_END.  With Z_SYNC_FLUSH on small
   * tiles (e.g. 12 KB << 64 KB default buffer), onData is never called and the
   * decompressed data stays in strm.output.  We extract it directly after push.
   */
  private decompressStream(streamIdx: number, compressed: Uint8Array): Uint8Array {
    const inflater = this.streams[streamIdx];
    if (!inflater) throw new Error('TightDecoder not initialized');

    // Collect output chunks produced by onData (called when internal buffer fills)
    const chunks: Uint8Array[] = [];
    const origOnData = inflater.onData;
    inflater.onData = (chunk: Uint8Array) => { chunks.push(chunk.slice()); };

    inflater.push(compressed, 2 /* Z_SYNC_FLUSH */);

    inflater.onData = origOnData;

    if (inflater.err) {
      throw new Error(`Tight inflate error (stream ${streamIdx}): ${inflater.msg}`);
    }

    // Extract any remaining data from pako's internal output buffer that
    // wasn't flushed via onData (the common case for small tiles).
    const strm = (inflater as any).strm;
    if (strm && strm.next_out > 0) {
      chunks.push(new Uint8Array(strm.output.buffer, strm.output.byteOffset, strm.next_out).slice());
    }

    if (chunks.length === 0) return new Uint8Array(0);
    if (chunks.length === 1) return chunks[0];
    const total = chunks.reduce((s, c) => s + c.length, 0);
    const result = new Uint8Array(total);
    let off = 0;
    for (const c of chunks) {
      result.set(c, off);
      off += c.length;
    }
    return result;
  }

  async decode(fb: Framebuffer, header: RectHeader, data: DataView): Promise<void> {
    if (!this.pako) return;

    const { x, y, width, height } = header;
    let offset = 0;

    for (let ty = 0; ty < height; ty += 64) {
      const tileH = Math.min(64, height - ty);
      for (let tx = 0; tx < width; tx += 64) {
        const tileW = Math.min(64, width - tx);
        const control = data.getUint8(offset);
        offset += 1;

        // Handle reset stream bits (bits 4-7). When set, the corresponding
        // zlib stream must be reset (new Inflate instance with fresh state).
        for (let s = 0; s < 4; s++) {
          if (control & (1 << (s + 4))) {
            this.streams[s] = new this.pako.Inflate();
          }
        }

        const subType = control & 0x0f;

        if (subType === 0x08) {
          // Solid fill: 3 bytes RGB
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
          // Basic compression (bits 0-1 = stream index)
          const streamIdx = subType & 0x03;
          const { length, bytesRead } = readCompactLength(data, offset);
          offset += bytesRead;

          const compressed = new Uint8Array(data.buffer, data.byteOffset + offset, length);
          offset += length;

          const decompressed = this.decompressStream(streamIdx, compressed);

          // Tight basic uses 3-byte RGB pixels (CPIXEL for 32bpp/24depth)
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
