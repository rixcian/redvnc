import type { Framebuffer } from '../framebuffer';
import type { RectHeader } from '../types';
import { readCompactLength } from '../rfb-parser';

/**
 * Decode Tight encoding on the main thread with minimal allocations.
 *
 * Key optimizations:
 * - Direct framebuffer writes (writeRectRGB/fillRect) avoid intermediate buffers
 * - JPEG decodes collected and resolved in parallel via Promise.allSettled
 * - Zlib stream output position reset avoids stale data (pako 2.x workaround)
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

  private decompressStream(streamIdx: number, compressed: Uint8Array): Uint8Array {
    const inflater = this.streams[streamIdx];
    if (!inflater) throw new Error('TightDecoder not initialized');

    const strm = (inflater as any).strm;

    if (strm && strm.output) {
      strm.next_out = 0;
      strm.avail_out = strm.output.length;
    }

    const chunks: Uint8Array[] = [];
    const origOnData = inflater.onData;
    inflater.onData = (chunk: Uint8Array) => { chunks.push(chunk.slice()); };

    inflater.push(compressed, 2 /* Z_SYNC_FLUSH */);

    inflater.onData = origOnData;

    if (inflater.err) {
      throw new Error(`Tight inflate error (stream ${streamIdx}): ${inflater.msg}`);
    }

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

    const jpegTasks: Array<{ promise: Promise<ImageBitmap>; dx: number; dy: number; w: number; h: number }> = [];

    for (let ty = 0; ty < height; ty += 64) {
      const tileH = Math.min(64, height - ty);
      for (let tx = 0; tx < width; tx += 64) {
        const tileW = Math.min(64, width - tx);
        const control = data.getUint8(offset);
        offset += 1;

        for (let s = 0; s < 4; s++) {
          if (control & (1 << (s + 4))) {
            this.streams[s] = new this.pako.Inflate();
          }
        }

        const subType = control & 0x0f;

        if (subType === 0x08) {
          // Solid fill — direct to framebuffer, zero allocation
          const r = data.getUint8(offset);
          const g = data.getUint8(offset + 1);
          const b = data.getUint8(offset + 2);
          offset += 3;
          fb.fillRect(x + tx, y + ty, tileW, tileH, r, g, b);
        } else if (subType === 0x09) {
          // JPEG — defer to parallel batch
          const { length, bytesRead } = readCompactLength(data, offset);
          offset += bytesRead;

          const jpegData = new Uint8Array(data.buffer, data.byteOffset + offset, length);
          offset += length;

          const blob = new Blob([jpegData.slice()], { type: 'image/jpeg' });
          jpegTasks.push({
            promise: createImageBitmap(blob),
            dx: x + tx, dy: y + ty, w: tileW, h: tileH,
          });
        } else {
          // Basic zlib — decompress and write RGB directly to framebuffer
          const streamIdx = subType & 0x03;
          const { length, bytesRead } = readCompactLength(data, offset);
          offset += bytesRead;

          const compressed = new Uint8Array(data.buffer, data.byteOffset + offset, length);
          offset += length;

          const decompressed = this.decompressStream(streamIdx, compressed);
          fb.writeRectRGB(x + tx, y + ty, tileW, tileH, decompressed);
        }
      }
    }

    if (jpegTasks.length > 0) {
      const results = await Promise.allSettled(jpegTasks.map(t => t.promise));
      for (let i = 0; i < results.length; i++) {
        const result = results[i];
        if (result.status === 'fulfilled') {
          const bitmap = result.value;
          const task = jpegTasks[i];
          fb.drawBitmap(bitmap, task.dx, task.dy, task.w, task.h);
          bitmap.close();
        }
      }
    }
  }
}
