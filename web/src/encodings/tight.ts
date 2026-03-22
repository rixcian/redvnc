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

  // Diagnostics: track tile decode stats
  private _diagTotal = 0;
  private _diagSolid = 0;
  private _diagJpeg = 0;
  private _diagBasic = 0;
  private _diagBlack = 0;    // tiles where decoded data is all-zero RGB
  private _diagShort = 0;    // tiles where decompressed data was shorter than expected
  private _diagLastLog = 0;

  async init(): Promise<void> {
    this.pako = await import('pako');
    for (let i = 0; i < 4; i++) {
      this.streams[i] = new this.pako.Inflate();
    }
  }

  /** Decompress data using a persistent zlib stream. */
  private decompressStream(streamIdx: number, compressed: Uint8Array): Uint8Array {
    const inflater = this.streams[streamIdx];
    if (!inflater) throw new Error('TightDecoder not initialized');

    // Collect output chunks for this push only
    const chunks: Uint8Array[] = [];
    const origOnData = inflater.onData;
    inflater.onData = (chunk: Uint8Array) => { chunks.push(chunk.slice()); };

    inflater.push(compressed, 2 /* Z_SYNC_FLUSH */);

    inflater.onData = origOnData;

    if (inflater.err) {
      throw new Error(`Tight inflate error (stream ${streamIdx}): ${inflater.msg}`);
    }

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
        this._diagTotal++;

        if (subType === 0x08) {
          // Solid fill: 3 bytes RGB
          const r = data.getUint8(offset);
          const g = data.getUint8(offset + 1);
          const b = data.getUint8(offset + 2);
          offset += 3;
          this._diagSolid++;
          if (r === 0 && g === 0 && b === 0) this._diagBlack++;

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
          this._diagJpeg++;
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
          this._diagBasic++;

          const compressed = new Uint8Array(data.buffer, data.byteOffset + offset, length);
          offset += length;

          const decompressed = this.decompressStream(streamIdx, compressed);
          const expectedBytes = tileW * tileH * 3;
          if (decompressed.length < expectedBytes) {
            this._diagShort++;
            console.warn(
              `Tight: decompressed ${decompressed.length} bytes, expected ${expectedBytes}`,
              `tile(${x + tx},${y + ty}) ${tileW}x${tileH} stream=${streamIdx}`,
            );
          }

          // Check if decoded data is all-zero (would produce black tile)
          let allZero = true;
          for (let i = 0; i < Math.min(decompressed.length, 64); i++) {
            if (decompressed[i] !== 0) { allZero = false; break; }
          }
          if (allZero && decompressed.length > 0) this._diagBlack++;

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

        // Log periodic diagnostics
        const now = performance.now();
        if (now - this._diagLastLog > 5000) {
          this._diagLastLog = now;
          console.log(
            `[Tight diag] total=${this._diagTotal}`,
            `solid=${this._diagSolid} basic=${this._diagBasic} jpeg=${this._diagJpeg}`,
            `black=${this._diagBlack} short=${this._diagShort}`,
          );
        }
      }
    }
  }
}
