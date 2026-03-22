import type { Framebuffer } from '../framebuffer';
import type { RectHeader } from '../types';
import { readCompactLength } from '../rfb-parser';

interface TileUpdate {
  x: number;
  y: number;
  w: number;
  h: number;
  pixels: ArrayBuffer;
}

/**
 * Decode Tight encoding using a Web Worker.
 *
 * All pako zlib decompression and RGB→RGBA conversion runs off the main
 * thread, freeing it to process incoming WebSocket messages without blocking.
 * The worker maintains the 4 persistent zlib streams required by the Tight
 * encoding specification.
 *
 * Falls back to synchronous main-thread decoding if the worker fails to
 * initialize (e.g. in environments without Worker support).
 */
export class TightDecoder {
  private worker: Worker | null = null;
  private pending = new Map<number, { resolve: () => void; reject: (err: Error) => void }>();
  private nextId = 0;
  private fb: Framebuffer | null = null;
  private workerReady = false;

  // Fallback: main-thread decoder (used if worker unavailable)
  private pako: typeof import('pako') | null = null;
  private streams: (import('pako').Inflate | null)[] = [null, null, null, null];

  async init(): Promise<void> {
    try {
      this.worker = new Worker(
        new URL('./tight-worker.ts', import.meta.url),
        { type: 'module' },
      );

      // Set up message handler
      this.worker.onmessage = (e: MessageEvent) => {
        const msg = e.data;

        if (msg.type === 'ready') {
          this.workerReady = true;
          return;
        }

        if (msg.type === 'decoded') {
          const { id, tiles } = msg as { id: number; tiles: TileUpdate[] };
          if (this.fb) {
            for (const tile of tiles) {
              this.fb.writeRect(tile.x, tile.y, tile.w, tile.h, new Uint8Array(tile.pixels));
            }
          }
          this.pending.get(id)?.resolve();
          this.pending.delete(id);
          return;
        }

        if (msg.type === 'error') {
          const { id, error } = msg;
          console.warn('Tight worker decode error:', error);
          this.pending.get(id)?.resolve(); // resolve, not reject — don't break FBU chain
          this.pending.delete(id);
        }
      };

      this.worker.onerror = (err) => {
        console.warn('Tight worker error, falling back to main thread:', err);
        this.workerReady = false;
        this.worker = null;
        // Resolve all pending requests so the FBU pipeline doesn't stall
        for (const [, p] of this.pending) p.resolve();
        this.pending.clear();
      };

      // Initialize pako in the worker
      this.worker.postMessage({ type: 'init' });

      // Wait for worker to be ready (with timeout)
      await new Promise<void>((resolve) => {
        const check = () => {
          if (this.workerReady) { resolve(); return; }
          setTimeout(check, 5);
        };
        check();
        // Timeout after 3 seconds — fall back to main thread
        setTimeout(() => { resolve(); }, 3000);
      });
    } catch {
      console.warn('Failed to create Tight worker, using main thread fallback');
      this.worker = null;
    }

    // Always init fallback decoder (used if worker fails mid-session)
    this.pako = await import('pako');
    for (let i = 0; i < 4; i++) {
      this.streams[i] = new this.pako.Inflate();
    }
  }

  /**
   * Reset decoder state (e.g. on reconnect).
   */
  reset(): void {
    if (this.worker && this.workerReady) {
      this.worker.postMessage({ type: 'reset' });
    }
    if (this.pako) {
      for (let i = 0; i < 4; i++) {
        this.streams[i] = new this.pako.Inflate();
      }
    }
  }

  async decode(fb: Framebuffer, header: RectHeader, data: DataView): Promise<void> {
    this.fb = fb;

    if (this.worker && this.workerReady) {
      return this.decodeInWorker(header, data);
    }

    // Fallback: main-thread decode
    return this.decodeMainThread(fb, header, data);
  }

  private decodeInWorker(header: RectHeader, data: DataView): Promise<void> {
    return new Promise<void>((resolve, reject) => {
      const id = this.nextId++;

      // Copy the rect data into a transferable ArrayBuffer
      const buf = data.buffer.slice(data.byteOffset, data.byteOffset + data.byteLength);

      this.pending.set(id, { resolve, reject });

      this.worker!.postMessage(
        {
          type: 'decode',
          id,
          x: header.x,
          y: header.y,
          width: header.width,
          height: header.height,
          data: buf,
        },
        [buf],
      );
    });
  }

  // ── Main-thread fallback (same logic as before worker migration) ──

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

  private async decodeMainThread(fb: Framebuffer, header: RectHeader, data: DataView): Promise<void> {
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
          const r = data.getUint8(offset);
          const g = data.getUint8(offset + 1);
          const b = data.getUint8(offset + 2);
          offset += 3;
          fb.fillRect(x + tx, y + ty, tileW, tileH, r, g, b);
        } else if (subType === 0x09) {
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
