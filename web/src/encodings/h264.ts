/**
 * H.264 decoder using the WebCodecs VideoDecoder API.
 *
 * Wire format per rectangle:
 *   [4 bytes] flags (uint32 BE) - bit 0 = keyframe (IDR)
 *   [4 bytes] NAL data length (uint32 BE)
 *   [N bytes] H.264 NAL units (Annex B format)
 */
import type { Framebuffer } from '../framebuffer';
import type { RectHeader } from '../types';

export class H264Decoder {
  private decoder: VideoDecoder | null = null;
  private fb: Framebuffer | null = null;
  private configuredWidth = 0;
  private configuredHeight = 0;
  private pendingResolve: (() => void) | null = null;

  /**
   * Returns true if WebCodecs VideoDecoder is available in this browser.
   */
  static isSupported(): boolean {
    return typeof VideoDecoder !== 'undefined';
  }

  async init(): Promise<void> {
    // Nothing to do until we get the first frame (we need dimensions to configure).
  }

  /**
   * Decode an H.264 rectangle and write the result to the framebuffer.
   */
  async decode(fb: Framebuffer, header: RectHeader, data: DataView): Promise<void> {
    const flags = data.getUint32(0);
    const nalLen = data.getUint32(4);
    const isKeyframe = (flags & 1) !== 0;

    const nalData = new Uint8Array(data.buffer, data.byteOffset + 8, nalLen);

    // Lazily create/reconfigure the decoder when dimensions change.
    if (
      this.decoder === null ||
      this.fb !== fb ||
      this.configuredWidth !== header.width ||
      this.configuredHeight !== header.height
    ) {
      this.reset();
      this.fb = fb;
      this.configuredWidth = header.width;
      this.configuredHeight = header.height;

      // Choose codec string based on resolution.
      // Baseline Level 3.0 for <= 720p, High Level 4.0 for larger.
      const codec = header.width * header.height > 1280 * 720
        ? 'avc1.640028'  // High L4.0
        : 'avc1.42001e'; // Baseline L3.0

      this.decoder = new VideoDecoder({
        output: (frame: VideoFrame) => {
          this.handleFrame(frame);
        },
        error: (err: DOMException) => {
          console.error('H264Decoder error:', err);
        },
      });

      this.decoder.configure({
        codec,
        optimizeForLatency: true,
      });
    }

    // Create a promise that resolves when the decoded frame is written.
    const framePromise = new Promise<void>((resolve) => {
      this.pendingResolve = resolve;
    });

    const chunk = new EncodedVideoChunk({
      type: isKeyframe ? 'key' : 'delta',
      timestamp: 0, // We don't need presentation timestamps for VNC.
      data: nalData,
    });

    this.decoder.decode(chunk);
    await this.decoder.flush();

    // Wait for the frame callback to complete.
    await framePromise;
  }

  private handleFrame(frame: VideoFrame): void {
    if (!this.fb) {
      frame.close();
      this.pendingResolve?.();
      this.pendingResolve = null;
      return;
    }

    const w = frame.displayWidth;
    const h = frame.displayHeight;

    // Use createImageBitmap + drawBitmap — this is the same proven path used
    // by Tight JPEG decoding.  drawBitmap internally does drawImage → getImageData
    // → writeRect, which produces correct RGBA pixels without any manual swap.
    createImageBitmap(frame).then((bitmap) => {
      frame.close();
      this.fb!.drawBitmap(bitmap, 0, 0, w, h);
      bitmap.close();

      this.pendingResolve?.();
      this.pendingResolve = null;
    }).catch(() => {
      frame.close();
      this.pendingResolve?.();
      this.pendingResolve = null;
    });
  }

  reset(): void {
    if (this.decoder) {
      try {
        this.decoder.close();
      } catch {
        // Ignore errors on close (decoder may already be in error state).
      }
      this.decoder = null;
    }
    this.fb = null;
    this.configuredWidth = 0;
    this.configuredHeight = 0;
    this.pendingResolve = null;
  }
}
