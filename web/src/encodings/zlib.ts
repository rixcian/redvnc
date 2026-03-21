import type { Framebuffer } from '../framebuffer';
import type { RectHeader } from '../types';

/**
 * Persistent zlib decompressor. The RFB spec requires the zlib stream to persist
 * across multiple rectangles within a connection.
 */
export class ZlibDecoder {
  private inflate: import('pako').Inflate | null = null;

  async init(): Promise<void> {
    const pako = await import('pako');
    this.inflate = new pako.Inflate();
  }

  decode(fb: Framebuffer, header: RectHeader, data: DataView): void {
    if (!this.inflate) {
      throw new Error('ZlibDecoder not initialized. Call init() first.');
    }

    const { x, y, width, height } = header;
    const compressedLen = data.getUint32(0);
    const compressed = new Uint8Array(data.buffer, data.byteOffset + 4, compressedLen);

    const chunks: Uint8Array[] = [];
    const origOnData = this.inflate.onData;
    this.inflate.onData = (chunk: Uint8Array) => { chunks.push(chunk.slice()); };

    this.inflate.push(compressed, 2 /* Z_SYNC_FLUSH */);

    this.inflate.onData = origOnData;

    if (this.inflate.err) {
      throw new Error(`Zlib inflate error: ${this.inflate.msg}`);
    }

    let decompressed: Uint8Array;
    if (chunks.length === 1) {
      decompressed = chunks[0];
    } else {
      const total = chunks.reduce((s, c) => s + c.length, 0);
      decompressed = new Uint8Array(total);
      let off = 0;
      for (const c of chunks) {
        decompressed.set(c, off);
        off += c.length;
      }
    }

    fb.writeRect(x, y, width, height, decompressed);
  }
}
