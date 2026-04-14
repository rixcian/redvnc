/**
 * H.264 decoder using the WebCodecs VideoDecoder API.
 *
 * Wire format per rectangle:
 *   [4 bytes] flags (uint32 BE) - bit 0 = keyframe (IDR)
 *   [4 bytes] NAL data length (uint32 BE)
 *   [N bytes] H.264 NAL units (Annex B format)
 *
 * This decoder is non-blocking: decode() submits frames to the VideoDecoder
 * and returns immediately. The decoded frame is written to the framebuffer
 * asynchronously via the output callback. Stale frames are dropped using a
 * generation counter — only the latest submitted frame is rendered.
 */
import type { Framebuffer } from '../framebuffer';
import type { RectHeader } from '../types';

export class H264Decoder {
  private decoder: VideoDecoder | null = null;
  private fb: Framebuffer | null = null;
  private configuredWidth = 0;
  private configuredHeight = 0;

  /** Incremented on each decode() call; only the latest generation is rendered. */
  private decodeGeneration = 0;

  /** Reusable OffscreenCanvas for extracting pixels from decoded VideoFrames. */
  private renderCanvas: OffscreenCanvas | null = null;
  private renderCtx: OffscreenCanvasRenderingContext2D | null = null;

  /**
   * Called when a decoded frame is actually written to the framebuffer.
   * Used by VncClient to record accurate rendered-FPS and end-to-end latency.
   */
  onFrameRendered: ((renderTime: number) => void) | null = null;

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
   * Submit an H.264 frame for decoding. Returns immediately — the decoded
   * frame will be written to the framebuffer asynchronously via the output
   * callback. Stale frames are dropped if a newer decode() call arrives
   * before the previous frame is rendered.
   */
  decode(fb: Framebuffer, header: RectHeader, data: DataView): void {
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

    this.fb = fb;
    this.decodeGeneration++;

    const chunk = new EncodedVideoChunk({
      type: isKeyframe ? 'key' : 'delta',
      // Use generation counter as timestamp so we can identify stale frames
      // in the output callback. VideoDecoder preserves this value on output.
      timestamp: this.decodeGeneration,
      data: nalData,
    });

    this.decoder.decode(chunk);
    // No flush() — let the decoder output frames asynchronously.
    // optimizeForLatency: true ensures frames are emitted ASAP without
    // internal buffering or reordering.
  }

  private handleFrame(frame: VideoFrame): void {
    if (!this.fb) {
      frame.close();
      return;
    }

    // Drop stale frames — only render the latest submitted generation.
    // When the client is slower than the server, intermediate frames
    // accumulate in the VideoDecoder queue. Rendering all of them would
    // multiply the delay; dropping them keeps us current.
    if (frame.timestamp !== this.decodeGeneration) {
      frame.close();
      return;
    }

    const w = frame.displayWidth;
    const h = frame.displayHeight;

    // Draw VideoFrame directly to OffscreenCanvas (no createImageBitmap needed).
    // CanvasRenderingContext2D.drawImage() accepts VideoFrame natively.
    if (!this.renderCanvas || this.renderCanvas.width !== w || this.renderCanvas.height !== h) {
      this.renderCanvas = new OffscreenCanvas(w, h);
      this.renderCtx = this.renderCanvas.getContext('2d', { willReadFrequently: true })!;
    }

    this.renderCtx!.drawImage(frame, 0, 0);
    frame.close();

    const imgData = this.renderCtx!.getImageData(0, 0, w, h);
    this.fb.writeRect(0, 0, w, h, imgData.data);

    this.onFrameRendered?.(performance.now());
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
    this.decodeGeneration = 0;
    this.renderCanvas = null;
    this.renderCtx = null;
  }
}
