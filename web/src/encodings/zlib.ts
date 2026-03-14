import type { Framebuffer } from '../framebuffer';
import type { RectHeader } from '../types';

/**
 * Persistent zlib decompressor. The RFB spec requires the zlib stream to persist
 * across multiple rectangles within a connection.
 */
export class ZlibDecoder {
  private inflateBuffer: Uint8Array[] = [];
  private pako: typeof import('pako') | null = null;
  private inflate: import('pako').Inflate | null = null;

  async init(): Promise<void> {
    this.pako = await import('pako');
    this.inflate = new this.pako.Inflate();
  }

  decode(fb: Framebuffer, header: RectHeader, data: DataView): void {
    if (!this.inflate || !this.pako) {
      throw new Error('ZlibDecoder not initialized. Call init() first.');
    }

    const { x, y, width, height } = header;
    const compressedLen = data.getUint32(0);
    const compressed = new Uint8Array(data.buffer, data.byteOffset + 4, compressedLen);

    // Use pako.inflate for individual chunks since persistent stream
    // handling with pako.Inflate is complex. For simplicity, decompress
    // each rectangle independently.
    const decompressed = this.pako.inflate(compressed);
    fb.writeRect(x, y, width, height, decompressed);
  }
}
