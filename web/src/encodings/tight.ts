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

  // Pre-allocated tile buffer to avoid per-tile allocation (max 64x64 RGBA)
  private tileBuffer = new Uint8Array(64 * 64 * 4);
  private tileBuffer32 = new Uint32Array(this.tileBuffer.buffer);

  async init(): Promise<void> {
    this.pako = await import('pako');
    for (let i = 0; i < 4; i++) {
      this.streams[i] = new this.pako.Inflate();
    }
  }

  /**
   * Decompress data using a persistent zlib stream.
   *
   * pako 2.x quirks with Z_SYNC_FLUSH that we must work around:
   *
   * 1. onData is only called when the internal 64KB output buffer is completely
   *    full (avail_out === 0) or on Z_STREAM_END.  Small tiles (~12KB) never
   *    trigger onData — the data stays in strm.output.
   *
   * 2. strm.next_out and strm.avail_out are NOT reset between push() calls.
   *    They accumulate across pushes, so without intervention, output from
   *    previous tiles bleeds into the current extraction window.
   *
   * Fix: reset strm.next_out/avail_out before each push so output always
   * starts at offset 0, then extract strm.output[0..next_out] after push.
   * The zlib dictionary state (strm.state) is unaffected by this reset.
   */
  private decompressStream(streamIdx: number, compressed: Uint8Array): Uint8Array {
    const inflater = this.streams[streamIdx];
    if (!inflater) throw new Error('TightDecoder not initialized');

    const strm = (inflater as any).strm;

    // Reset the output write position so this push's data starts at offset 0.
    // Without this, next_out accumulates across pushes and we'd extract stale
    // data from previous tiles along with the current tile's data.
    if (strm && strm.output) {
      strm.next_out = 0;
      strm.avail_out = strm.output.length;
    }

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

    // Collect JPEG decode promises to resolve in parallel instead of
    // awaiting each one sequentially. This is the single biggest perf win:
    // at 1920x1080 with 510 tiles, sequential awaits take ~500-1000ms
    // while parallel decodes complete in ~20-50ms total.
    const jpegTasks: Array<{ promise: Promise<ImageBitmap>; dx: number; dy: number; w: number; h: number }> = [];

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
          // Solid fill: use Uint32Array.fill for ~4x speedup over per-pixel loop
          const r = data.getUint8(offset);
          const g = data.getUint8(offset + 1);
          const b = data.getUint8(offset + 2);
          offset += 3;

          const pixelCount = tileW * tileH;
          // Pack RGBA into a single 32-bit value (little-endian: ABGR)
          const rgba32 = (255 << 24) | (b << 16) | (g << 8) | r;
          this.tileBuffer32.fill(rgba32, 0, pixelCount);
          fb.writeRect(x + tx, y + ty, tileW, tileH, this.tileBuffer.subarray(0, pixelCount * 4));
        } else if (subType === 0x09) {
          // JPEG: parse data now but defer decode to parallel batch
          const { length, bytesRead } = readCompactLength(data, offset);
          offset += bytesRead;

          const jpegData = new Uint8Array(data.buffer, data.byteOffset + offset, length);
          offset += length;

          const blob = new Blob([jpegData.slice()], { type: 'image/jpeg' });
          jpegTasks.push({
            promise: createImageBitmap(blob),
            dx: x + tx,
            dy: y + ty,
            w: tileW,
            h: tileH,
          });
        } else {
          // Basic compression (bits 0-1 = stream index)
          const streamIdx = subType & 0x03;
          const { length, bytesRead } = readCompactLength(data, offset);
          offset += bytesRead;

          const compressed = new Uint8Array(data.buffer, data.byteOffset + offset, length);
          offset += length;

          const decompressed = this.decompressStream(streamIdx, compressed);

          // Tight basic uses 3-byte RGB pixels (CPIXEL for 32bpp/24depth).
          // Write into pre-allocated tile buffer to avoid per-tile allocation.
          const pixelCount = tileW * tileH;
          const buf = this.tileBuffer;
          for (let i = 0; i < pixelCount; i++) {
            const i3 = i * 3;
            const i4 = i * 4;
            buf[i4] = decompressed[i3];
            buf[i4 + 1] = decompressed[i3 + 1];
            buf[i4 + 2] = decompressed[i3 + 2];
            buf[i4 + 3] = 255;
          }
          fb.writeRect(x + tx, y + ty, tileW, tileH, buf.subarray(0, pixelCount * 4));
        }
      }
    }

    // Resolve all JPEG decodes in parallel
    if (jpegTasks.length > 0) {
      const results = await Promise.allSettled(jpegTasks.map(t => t.promise));
      for (let i = 0; i < results.length; i++) {
        const result = results[i];
        if (result.status === 'fulfilled') {
          const bitmap = result.value;
          const task = jpegTasks[i];
          fb.drawBitmap(bitmap, task.dx, task.dy, task.w, task.h);
          bitmap.close();
        } else {
          console.warn('JPEG tile decode failed:', result.reason);
        }
      }
    }
  }
}
