import type { Framebuffer } from './framebuffer';

/**
 * Common interface for both Canvas2D and WebGL renderers.
 */
export interface IRenderer {
  attach(canvas: HTMLCanvasElement): void;
  detach(): void;
  readonly attached: boolean;
  setScaleToFit(scale: boolean): void;
  updateCanvasSize(fb: Framebuffer): void;
  render(fb: Framebuffer): void;
  /**
   * Direct GPU upload path for video frames (H.264 fast path).
   * Returns true if the frame was rendered directly (skipping the
   * CPU-side framebuffer). Returns false if the renderer can't handle
   * the frame (e.g. not attached, size mismatch) — caller should fall
   * back to the CPU readback path.
   *
   * fbWidth/fbHeight are the logical framebuffer dimensions the renderer
   * expects. WebGLRenderer uses them to skip the fast path when the
   * decoded frame doesn't match the pre-allocated texture size.
   */
  renderVideoFrame(frame: VideoFrame, fbWidth: number, fbHeight: number): boolean;
  setCursor(imageData: Uint8Array, width: number, height: number, hotX: number, hotY: number): void;
  translateCoordinates(event: MouseEvent, fbWidth: number, fbHeight: number): { x: number; y: number };
  /** Same mapping as translateCoordinates, for touch / programmatic use. */
  translatePointer(clientX: number, clientY: number, fbWidth: number, fbHeight: number): { x: number; y: number };
}
