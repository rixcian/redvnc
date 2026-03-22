/**
 * Web Worker for Tight encoding decode. Moves all pako zlib decompression
 * and RGB→RGBA conversion off the main thread, freeing it to process
 * incoming WebSocket messages without blocking.
 *
 * The worker maintains 4 persistent pako inflate streams (per RFB spec).
 * It receives compressed rect data, decodes all tiles, and transfers back
 * a single RGBA buffer per rect.
 */

import type pako from 'pako';

let pakoModule: typeof pako;
const streams: (pako.Inflate | null)[] = [null, null, null, null];

function readCompactLength(view: DataView, offset: number): { length: number; bytesRead: number } {
  let length = view.getUint8(offset) & 0x7f;
  let bytesRead = 1;

  if (view.getUint8(offset) & 0x80) {
    length |= (view.getUint8(offset + 1) & 0x7f) << 7;
    bytesRead = 2;
    if (view.getUint8(offset + 1) & 0x80) {
      length |= view.getUint8(offset + 2) << 14;
      bytesRead = 3;
    }
  }

  return { length, bytesRead };
}

function decompressStream(streamIdx: number, compressed: Uint8Array): Uint8Array {
  const inflater = streams[streamIdx];
  if (!inflater) throw new Error('Stream not initialized');

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

interface TileUpdate {
  x: number;
  y: number;
  w: number;
  h: number;
  pixels: ArrayBuffer;
}

interface JpegTileUpdate {
  x: number;
  y: number;
  w: number;
  h: number;
  bitmap: ImageBitmap;
}

// Reusable OffscreenCanvas for JPEG pixel extraction in worker
let jpegCanvas: OffscreenCanvas | null = null;
let jpegCtx: OffscreenCanvasRenderingContext2D | null = null;

async function decodeRect(
  x: number, y: number, width: number, height: number,
  data: DataView,
): Promise<{ tiles: TileUpdate[] }> {
  let offset = 0;
  const tiles: TileUpdate[] = [];
  const jpegTasks: Array<{ promise: Promise<ImageBitmap>; dx: number; dy: number; w: number; h: number }> = [];

  for (let ty = 0; ty < height; ty += 64) {
    const tileH = Math.min(64, height - ty);
    for (let tx = 0; tx < width; tx += 64) {
      const tileW = Math.min(64, width - tx);
      const control = data.getUint8(offset);
      offset += 1;

      // Handle reset stream bits (bits 4-7)
      for (let s = 0; s < 4; s++) {
        if (control & (1 << (s + 4))) {
          streams[s] = new pakoModule.Inflate();
        }
      }

      const subType = control & 0x0f;

      if (subType === 0x08) {
        // Solid fill
        const r = data.getUint8(offset);
        const g = data.getUint8(offset + 1);
        const b = data.getUint8(offset + 2);
        offset += 3;

        const pixelCount = tileW * tileH;
        const pixels = new Uint8Array(pixelCount * 4);
        const pixels32 = new Uint32Array(pixels.buffer);
        const rgba32 = (255 << 24) | (b << 16) | (g << 8) | r;
        pixels32.fill(rgba32);

        tiles.push({ x: x + tx, y: y + ty, w: tileW, h: tileH, pixels: pixels.buffer });
      } else if (subType === 0x09) {
        // JPEG: parse data, defer decode to parallel batch
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
        // Basic compression
        const streamIdx = subType & 0x03;
        const { length, bytesRead } = readCompactLength(data, offset);
        offset += bytesRead;

        const compressed = new Uint8Array(data.buffer, data.byteOffset + offset, length);
        offset += length;

        const decompressed = decompressStream(streamIdx, compressed);

        // RGB→RGBA conversion
        const pixelCount = tileW * tileH;
        const pixels = new Uint8Array(pixelCount * 4);
        for (let i = 0; i < pixelCount; i++) {
          const i3 = i * 3;
          const i4 = i * 4;
          pixels[i4] = decompressed[i3];
          pixels[i4 + 1] = decompressed[i3 + 1];
          pixels[i4 + 2] = decompressed[i3 + 2];
          pixels[i4 + 3] = 255;
        }

        tiles.push({ x: x + tx, y: y + ty, w: tileW, h: tileH, pixels: pixels.buffer });
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

        // Extract pixels from bitmap using OffscreenCanvas
        if (!jpegCanvas || jpegCanvas.width < task.w || jpegCanvas.height < task.h) {
          jpegCanvas = new OffscreenCanvas(task.w, task.h);
          jpegCtx = jpegCanvas.getContext('2d', { willReadFrequently: true })!;
        }
        jpegCtx!.drawImage(bitmap, 0, 0);
        const imgData = jpegCtx!.getImageData(0, 0, task.w, task.h);
        bitmap.close();

        // Copy to transferable buffer
        const pixels = new Uint8Array(imgData.data.buffer.slice(0));
        tiles.push({ x: task.dx, y: task.dy, w: task.w, h: task.h, pixels: pixels.buffer });
      }
    }
  }

  return { tiles };
}

// Message handler
self.onmessage = async (e: MessageEvent) => {
  const msg = e.data;

  if (msg.type === 'init') {
    // Initialize pako
    pakoModule = await import('pako');
    for (let i = 0; i < 4; i++) {
      streams[i] = new pakoModule.Inflate();
    }
    (self as any).postMessage({ type: 'ready' });
    return;
  }

  if (msg.type === 'reset') {
    // Reset all streams (e.g. on reconnect)
    if (pakoModule) {
      for (let i = 0; i < 4; i++) {
        streams[i] = new pakoModule.Inflate();
      }
    }
    return;
  }

  if (msg.type === 'decode') {
    const { id, x, y, width, height } = msg;
    const data = new DataView(msg.data);

    try {
      const { tiles } = await decodeRect(x, y, width, height, data);

      // Transfer all pixel buffers (zero-copy)
      const transferables = tiles.map(t => t.pixels);
      (self as any).postMessage({ type: 'decoded', id, tiles }, transferables);
    } catch (err) {
      (self as any).postMessage({
        type: 'error',
        id,
        error: err instanceof Error ? err.message : String(err),
      });
    }
  }
};
