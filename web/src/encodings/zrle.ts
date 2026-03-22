import { Inflate } from 'fflate';
import type { Framebuffer } from '../framebuffer';
import type { RectHeader } from '../types';

const TILE_SIZE = 64;

/**
 * ZRLE decoder. Maintains a persistent zlib decompressor across rectangles.
 */
export class ZrleDecoder {
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
      throw new Error('ZrleDecoder not initialized. Call init() first.');
    }

    const { x, y, width, height } = header;
    const compressedLen = data.getUint32(0);
    let compressed = new Uint8Array(data.buffer, data.byteOffset + 4, compressedLen);

    // Skip the 2-byte zlib header on the first push
    if (this.firstPush) {
      compressed = compressed.subarray(2);
      this.firstPush = false;
    }

    // Decompress
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

    let offset = 0;

    // Process 64x64 tiles
    for (let tileY = y; tileY < y + height; tileY += TILE_SIZE) {
      const tileH = Math.min(TILE_SIZE, y + height - tileY);
      for (let tileX = x; tileX < x + width; tileX += TILE_SIZE) {
        const tileW = Math.min(TILE_SIZE, x + width - tileX);
        offset = this.decodeTile(fb, decompressed, offset, tileX, tileY, tileW, tileH);
      }
    }
  }

  private decodeTile(
    fb: Framebuffer,
    data: Uint8Array,
    offset: number,
    tileX: number,
    tileY: number,
    tileW: number,
    tileH: number,
  ): number {
    const subtype = data[offset++];

    if (subtype === 0) {
      // Raw CPIXEL
      return this.decodeRawTile(fb, data, offset, tileX, tileY, tileW, tileH);
    } else if (subtype === 1) {
      // Solid
      return this.decodeSolidTile(fb, data, offset, tileX, tileY, tileW, tileH);
    } else if (subtype >= 2 && subtype <= 16) {
      // Packed palette
      return this.decodePackedPalette(fb, data, offset, tileX, tileY, tileW, tileH, subtype);
    } else if (subtype === 128) {
      // Plain RLE
      return this.decodePlainRLE(fb, data, offset, tileX, tileY, tileW, tileH);
    } else if (subtype >= 130) {
      // Palette RLE
      return this.decodePaletteRLE(fb, data, offset, tileX, tileY, tileW, tileH, subtype);
    }

    // Unknown subtype — skip as raw
    return this.decodeRawTile(fb, data, offset, tileX, tileY, tileW, tileH);
  }

  private decodeRawTile(
    fb: Framebuffer,
    data: Uint8Array,
    offset: number,
    tileX: number,
    tileY: number,
    tileW: number,
    tileH: number,
  ): number {
    for (let row = 0; row < tileH; row++) {
      for (let col = 0; col < tileW; col++) {
        const b = data[offset++];
        const g = data[offset++];
        const r = data[offset++];
        fb.setPixel(tileX + col, tileY + row, r, g, b, 255);
      }
    }
    return offset;
  }

  private decodeSolidTile(
    fb: Framebuffer,
    data: Uint8Array,
    offset: number,
    tileX: number,
    tileY: number,
    tileW: number,
    tileH: number,
  ): number {
    const b = data[offset++];
    const g = data[offset++];
    const r = data[offset++];

    for (let row = 0; row < tileH; row++) {
      for (let col = 0; col < tileW; col++) {
        fb.setPixel(tileX + col, tileY + row, r, g, b, 255);
      }
    }
    return offset;
  }

  private decodePackedPalette(
    fb: Framebuffer,
    data: Uint8Array,
    offset: number,
    tileX: number,
    tileY: number,
    tileW: number,
    tileH: number,
    paletteSize: number,
  ): number {
    // Read palette
    const palette: [number, number, number][] = [];
    for (let i = 0; i < paletteSize; i++) {
      const b = data[offset++];
      const g = data[offset++];
      const r = data[offset++];
      palette.push([r, g, b]);
    }

    // Determine bits per index
    let bitsPerIndex: number;
    if (paletteSize === 2) bitsPerIndex = 1;
    else if (paletteSize <= 4) bitsPerIndex = 2;
    else bitsPerIndex = 4;

    const mask = (1 << bitsPerIndex) - 1;

    // Read packed indices row by row
    for (let row = 0; row < tileH; row++) {
      let currentByte = 0;
      let bitsRemaining = 0;

      for (let col = 0; col < tileW; col++) {
        if (bitsRemaining === 0) {
          currentByte = data[offset++];
          bitsRemaining = 8;
        }

        bitsRemaining -= bitsPerIndex;
        const idx = (currentByte >> bitsRemaining) & mask;
        const [r, g, b] = palette[idx] || [0, 0, 0];
        fb.setPixel(tileX + col, tileY + row, r, g, b, 255);
      }
    }

    return offset;
  }

  private decodePlainRLE(
    fb: Framebuffer,
    data: Uint8Array,
    offset: number,
    tileX: number,
    tileY: number,
    tileW: number,
    tileH: number,
  ): number {
    const numPixels = tileW * tileH;
    let pixelsDone = 0;

    while (pixelsDone < numPixels) {
      const b = data[offset++];
      const g = data[offset++];
      const r = data[offset++];

      let runLength = 1;
      let runByte: number;
      do {
        runByte = data[offset++];
        runLength += runByte;
      } while (runByte === 255);

      for (let i = 0; i < runLength && pixelsDone < numPixels; i++, pixelsDone++) {
        const px = pixelsDone % tileW;
        const py = Math.floor(pixelsDone / tileW);
        fb.setPixel(tileX + px, tileY + py, r, g, b, 255);
      }
    }

    return offset;
  }

  private decodePaletteRLE(
    fb: Framebuffer,
    data: Uint8Array,
    offset: number,
    tileX: number,
    tileY: number,
    tileW: number,
    tileH: number,
    subtype: number,
  ): number {
    const paletteSize = subtype - 128;

    // Read palette
    const palette: [number, number, number][] = [];
    for (let i = 0; i < paletteSize; i++) {
      const b = data[offset++];
      const g = data[offset++];
      const r = data[offset++];
      palette.push([r, g, b]);
    }

    const numPixels = tileW * tileH;
    let pixelsDone = 0;

    while (pixelsDone < numPixels) {
      const idx = data[offset++];

      if (idx < 128) {
        // Single pixel
        const [r, g, b] = palette[idx] || [0, 0, 0];
        const px = pixelsDone % tileW;
        const py = Math.floor(pixelsDone / tileW);
        fb.setPixel(tileX + px, tileY + py, r, g, b, 255);
        pixelsDone++;
      } else {
        // RLE run
        const paletteIdx = idx - 128;
        let runLength = 1;
        let runByte: number;
        do {
          runByte = data[offset++];
          runLength += runByte;
        } while (runByte === 255);

        const [r, g, b] = palette[paletteIdx] || [0, 0, 0];
        for (let i = 0; i < runLength && pixelsDone < numPixels; i++, pixelsDone++) {
          const px = pixelsDone % tileW;
          const py = Math.floor(pixelsDone / tileW);
          fb.setPixel(tileX + px, tileY + py, r, g, b, 255);
        }
      }
    }

    return offset;
  }
}
