import { Inflate } from 'fflate';
import type { Framebuffer } from '../framebuffer';
import type { RectHeader } from '../types';
import { readCompactLength } from '../rfb-parser';

/**
 * A wrapper around fflate's streaming Inflate that handles VNC's zlib streams.
 *
 * VNC Tight encoding sends zlib-wrapped data (2-byte header on first chunk,
 * then raw DEFLATE blocks flushed with Z_SYNC_FLUSH). fflate's Inflate handles
 * raw DEFLATE, so we skip the zlib header on the first push for each stream.
 */
class ZlibStream {
  private inflater: Inflate;
  private chunks: Uint8Array[] = [];
  private firstPush = true;

  constructor() {
    this.inflater = new Inflate((chunk) => {
      this.chunks.push(chunk.slice());
    });
  }

  decompress(compressed: Uint8Array): Uint8Array {
    this.chunks.length = 0;

    let input = compressed;
    if (this.firstPush) {
      // Skip the 2-byte zlib header (CMF + FLG)
      input = compressed.subarray(2);
      this.firstPush = false;
    }

    this.inflater.push(input, false);

    if (this.chunks.length === 0) return new Uint8Array(0);
    if (this.chunks.length === 1) return this.chunks[0];

    const total = this.chunks.reduce((s, c) => s + c.length, 0);
    const result = new Uint8Array(total);
    let off = 0;
    for (const c of this.chunks) {
      result.set(c, off);
      off += c.length;
    }
    return result;
  }
}

/**
 * Decode Tight encoding on the main thread with minimal allocations.
 *
 * Key optimizations:
 * - fflate for zlib decompression (faster than pako, smaller bundle)
 * - Direct framebuffer writes (writeRectRGB/fillRect) avoid intermediate buffers
 * - JPEG decodes collected and resolved in parallel via Promise.allSettled
 */
export class TightDecoder {
  private streams: (ZlibStream | null)[] = [null, null, null, null];

  async init(): Promise<void> {
    for (let i = 0; i < 4; i++) {
      this.streams[i] = new ZlibStream();
    }
  }

  async decode(fb: Framebuffer, header: RectHeader, data: DataView): Promise<void> {
    const { x, y, width, height } = header;
    let offset = 0;

    const jpegTasks: Array<{ promise: Promise<ImageBitmap>; dx: number; dy: number; w: number; h: number }> = [];

    for (let ty = 0; ty < height; ty += 64) {
      const tileH = Math.min(64, height - ty);
      for (let tx = 0; tx < width; tx += 64) {
        const tileW = Math.min(64, width - tx);
        const control = data.getUint8(offset);
        offset += 1;

        // Check stream reset bits (bits 4-7)
        for (let s = 0; s < 4; s++) {
          if (control & (1 << (s + 4))) {
            this.streams[s] = new ZlibStream();
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
          const stream = this.streams[streamIdx];
          if (!stream) throw new Error(`TightDecoder stream ${streamIdx} not initialized`);

          const { length, bytesRead } = readCompactLength(data, offset);
          offset += bytesRead;

          const compressed = new Uint8Array(data.buffer, data.byteOffset + offset, length);
          offset += length;

          const decompressed = stream.decompress(compressed);
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
