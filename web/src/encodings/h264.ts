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
 * asynchronously via the output callback. Stale frames are dropped by
 * comparing each frame's submit timestamp against the latest — only the most
 * recently submitted frame is rendered.
 */
import type { Framebuffer } from '../framebuffer';
import type { RectHeader } from '../types';

export class H264Decoder {
  private decoder: VideoDecoder | null = null;
  private fb: Framebuffer | null = null;
  private configuredWidth = 0;
  private configuredHeight = 0;

  /**
   * Timestamp (microseconds) of the most recently submitted chunk.
   * Used to detect and drop stale frames when the decoder queue backs up.
   */
  private latestSubmitTimeMicros = 0;

  /**
   * Maps submit timestamp (microseconds) → FBU request time (ms).
   * Allows the onFrameRendered callback to compute accurate end-to-end
   * latency per rendered frame, even when frames are dropped out-of-order.
   */
  private pendingFrames = new Map<number, number>();

  /** Reusable OffscreenCanvas for extracting pixels from decoded VideoFrames. */
  private renderCanvas: OffscreenCanvas | null = null;
  private renderCtx: OffscreenCanvasRenderingContext2D | null = null;

  /**
   * Called when a decoded frame is actually written to the framebuffer.
   *   renderTime      — performance.now() at render time (ms)
   *   fbuRequestTime  — performance.now() when the FBU request was sent (ms)
   *   decodeLatencyMs — time from chunk submission to render (VideoDecoder + pixel readback)
   */
  onFrameRendered: ((renderTime: number, fbuRequestTime: number, decodeLatencyMs: number) => void) | null = null;

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
   * callback.
   *
   * @param fbuRequestTime — performance.now() timestamp when the FBU request
   *   that triggered this frame was sent. Used for end-to-end latency tracking.
   */
  decode(fb: Framebuffer, header: RectHeader, data: DataView, fbuRequestTime: number): void {
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

      this.decoder = new VideoDecoder({
        output: (frame: VideoFrame) => {
          this.handleFrame(frame);
        },
        error: (err: DOMException) => {
          console.error('H264Decoder error:', err);
        },
      });

      this.decoder.configure({
        // Always use Baseline profile — the server encodes Baseline on all
        // platforms (x264 with "baseline" preset, MFT with eAVEncH264VProfileBase=66).
        // Using a High-profile codec string causes some browsers to allocate a
        // B-frame reorder buffer (even with optimizeForLatency:true), adding
        // 200–800ms of decoder-side lag. Baseline has no B-frames by spec.
        codec: this.getCodecString(header.width, header.height),
        optimizeForLatency: true,
      });
    }

    this.fb = fb;

    // Use performance.now() in microseconds as the chunk timestamp.
    // This serves two purposes:
    //   1. Stale frame detection: a higher timestamp means a newer frame.
    //   2. Per-frame decode latency: renderTime - timestamp/1000 gives the
    //      VideoDecoder + pixel readback cost for that specific frame.
    const submitTimeMicros = Math.round(performance.now() * 1000);
    this.latestSubmitTimeMicros = submitTimeMicros;
    this.pendingFrames.set(submitTimeMicros, fbuRequestTime);

    const chunk = new EncodedVideoChunk({
      type: isKeyframe ? 'key' : 'delta',
      timestamp: submitTimeMicros,
      data: nalData,
    });

    this.decoder.decode(chunk);
    // No flush() — let the decoder output frames asynchronously.
    // optimizeForLatency: true ensures frames are emitted ASAP without
    // internal buffering or reordering.
  }

  /**
   * Returns the AVC codec string for the given resolution, always using
   * Baseline profile. Levels are chosen to just cover each resolution tier:
   *   ≤ 720p   → Baseline L3.1 (avc1.42E01F)
   *   ≤ 1080p  → Baseline L4.0 (avc1.42E028)
   *   ≤ 1440p  → Baseline L5.0 (avc1.42E032)
   *   > 1440p  → Baseline L5.1 (avc1.42E033)
   *
   * Codec string format: avc1.PPCCLL
   *   PP = profile_idc hex (42 = Baseline)
   *   CC = constraint flags hex (E0 = constraint_set0|1|2, required for Baseline)
   *   LL = level_idc hex (28=40=L4.0, 1F=31=L3.1, etc.)
   */
  private getCodecString(width: number, height: number): string {
    const pixels = width * height;
    if (pixels <= 1280 * 720)  return 'avc1.42E01F'; // Baseline L3.1
    if (pixels <= 1920 * 1080) return 'avc1.42E028'; // Baseline L4.0
    if (pixels <= 2560 * 1440) return 'avc1.42E032'; // Baseline L5.0
    return 'avc1.42E033';                             // Baseline L5.1
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
    if (frame.timestamp !== this.latestSubmitTimeMicros) {
      this.pendingFrames.delete(frame.timestamp);
      frame.close();
      return;
    }

    const fbuRequestTime = this.pendingFrames.get(frame.timestamp) ?? 0;
    this.pendingFrames.delete(frame.timestamp);

    // Purge any stale entries for frames older than this one.
    for (const ts of this.pendingFrames.keys()) {
      if (ts < frame.timestamp) {
        this.pendingFrames.delete(ts);
      }
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

    const renderTime = performance.now();
    // decodeLatencyMs = time from chunk submission to framebuffer write.
    // This covers: VideoDecoder internal processing + drawImage + getImageData.
    const decodeLatencyMs = renderTime - frame.timestamp / 1000;
    this.onFrameRendered?.(renderTime, fbuRequestTime, decodeLatencyMs);
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
    this.latestSubmitTimeMicros = 0;
    this.pendingFrames.clear();
    this.renderCanvas = null;
    this.renderCtx = null;
  }
}
