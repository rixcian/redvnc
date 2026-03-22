import type { Framebuffer } from './framebuffer';
import type { IRenderer } from './renderer-interface';

export class CanvasRenderer implements IRenderer {
  private canvas: HTMLCanvasElement | null = null;
  private ctx: CanvasRenderingContext2D | null = null;
  private scaleToFit: boolean;

  constructor(scaleToFit: boolean = false) {
    this.scaleToFit = scaleToFit;
  }

  attach(canvas: HTMLCanvasElement): void {
    this.canvas = canvas;
    this.ctx = canvas.getContext('2d', { alpha: false });
  }

  detach(): void {
    this.canvas = null;
    this.ctx = null;
  }

  get attached(): boolean {
    return this.canvas !== null;
  }

  setScaleToFit(scale: boolean): void {
    this.scaleToFit = scale;
  }

  /**
   * Update the canvas size to match the framebuffer dimensions.
   */
  updateCanvasSize(fb: Framebuffer): void {
    if (!this.canvas) return;

    // Always set canvas internal resolution to match the framebuffer.
    // When scaleToFit is true, CSS width/height: 100% handles visual scaling.
    this.canvas.width = fb.width;
    this.canvas.height = fb.height;
  }

  /**
   * Render dirty regions from the framebuffer to the canvas.
   *
   * Coalesces all dirty rectangles into a single bounding-box putImageData
   * call to avoid visual tearing when many small tiles update at once.
   */
  render(fb: Framebuffer): void {
    if (!this.ctx || !this.canvas) return;

    const dirtyRects = fb.dirtyRects;
    if (dirtyRects.length === 0) return;

    // Compute bounding box of all dirty rects
    let minX = dirtyRects[0].x;
    let minY = dirtyRects[0].y;
    let maxX = dirtyRects[0].x + dirtyRects[0].w;
    let maxY = dirtyRects[0].y + dirtyRects[0].h;

    for (let i = 1; i < dirtyRects.length; i++) {
      const r = dirtyRects[i];
      if (r.x < minX) minX = r.x;
      if (r.y < minY) minY = r.y;
      if (r.x + r.w > maxX) maxX = r.x + r.w;
      if (r.y + r.h > maxY) maxY = r.y + r.h;
    }

    // Single putImageData for the entire bounding box
    this.ctx.putImageData(
      fb.imageData,
      0, 0,
      minX, minY,
      maxX - minX, maxY - minY,
    );

    fb.clearDirty();
  }

  /**
   * Set a custom cursor on the canvas element.
   */
  setCursor(imageData: Uint8Array, width: number, height: number, hotX: number, hotY: number): void {
    if (!this.canvas) return;

    const cursorCanvas = document.createElement('canvas');
    cursorCanvas.width = width;
    cursorCanvas.height = height;
    const ctx = cursorCanvas.getContext('2d')!;
    const imgData = ctx.createImageData(width, height);
    imgData.data.set(imageData);
    ctx.putImageData(imgData, 0, 0);

    const dataUrl = cursorCanvas.toDataURL('image/png');
    this.canvas.style.cursor = `url(${dataUrl}) ${hotX} ${hotY}, auto`;
  }

  /**
   * Get the VNC coordinates from a mouse event, accounting for scaling.
   */
  translateCoordinates(
    event: MouseEvent,
    fbWidth: number,
    fbHeight: number,
  ): { x: number; y: number } {
    if (!this.canvas) return { x: 0, y: 0 };

    const rect = this.canvas.getBoundingClientRect();
    const canvasX = event.clientX - rect.left;
    const canvasY = event.clientY - rect.top;

    if (this.scaleToFit) {
      const scaleX = fbWidth / rect.width;
      const scaleY = fbHeight / rect.height;
      return {
        x: Math.floor(canvasX * scaleX),
        y: Math.floor(canvasY * scaleY),
      };
    }

    return {
      x: Math.floor(canvasX),
      y: Math.floor(canvasY),
    };
  }
}
