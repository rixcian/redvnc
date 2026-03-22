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
  setCursor(imageData: Uint8Array, width: number, height: number, hotX: number, hotY: number): void;
  translateCoordinates(event: MouseEvent, fbWidth: number, fbHeight: number): { x: number; y: number };
}
