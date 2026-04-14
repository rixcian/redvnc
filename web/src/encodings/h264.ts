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
  private canvas: OffscreenCanvas | null = null;
  private ctx: OffscreenCanvasRenderingContext2D | null = null;
  private pendingFrame: Promise<void> | null = null;
  private resolvePending: (() => void) | null = null;

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
      (this.canvas && (this.canvas.width !== header.width || this.canvas.height !== header.height))
    ) {
      this.reset();
      this.fb = fb;
      this.canvas = new OffscreenCanvas(header.width, header.height);
      this.ctx = this.canvas.getContext('2d');
      if (!this.ctx) {
        throw new Error('H264Decoder: failed to get 2d context from OffscreenCanvas');
      }

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
      this.resolvePending = resolve;
    });
    this.pendingFrame = framePromise;

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
    if (!this.ctx || !this.fb || !this.canvas) {
      frame.close();
      return;
    }

    const w = frame.displayWidth;
    const h = frame.displayHeight;

    // Resize canvas if needed.
    if (this.canvas.width !== w || this.canvas.height !== h) {
      this.canvas.width = w;
      this.canvas.height = h;
    }

    // Draw VideoFrame to canvas, then extract pixels.
    this.ctx.drawImage(frame, 0, 0);
    frame.close();

    const imageData = this.ctx.getImageData(0, 0, w, h);
    // imageData is RGBA; framebuffer expects BGRA via writeRect.
    // writeRect swaps B<->R, so we need to provide BGRA.
    // Since imageData is RGBA, we need to swap R and B before passing to writeRect.
    const pixels = imageData.data;
    for (let i = 0; i < pixels.length; i += 4) {
      const r = pixels[i];
      pixels[i] = pixels[i + 2];     // B
      pixels[i + 2] = r;              // R
    }

    this.fb.writeRect(0, 0, w, h, new Uint8Array(pixels.buffer));

    if (this.resolvePending) {
      this.resolvePending();
      this.resolvePending = null;
    }
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
    this.canvas = null;
    this.ctx = null;
    this.fb = null;
    this.pendingFrame = null;
    this.resolvePending = null;
  }
}
