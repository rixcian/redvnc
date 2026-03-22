export class Framebuffer {
  private _width: number;
  private _height: number;
  private _imageData: ImageData;
  private _dirtyRects: Array<{ x: number; y: number; w: number; h: number }> = [];

  // Reusable OffscreenCanvas for JPEG bitmap pixel extraction.
  // Avoids creating a new canvas per tile (~510 per full-screen FBU).
  private _bitmapCanvas: OffscreenCanvas | null = null;
  private _bitmapCtx: OffscreenCanvasRenderingContext2D | null = null;

  constructor(width: number, height: number) {
    this._width = width;
    this._height = height;
    this._imageData = new ImageData(width, height);
    // Fill with magenta so un-decoded tiles are visually obvious.
    // Black = server sent black data.  Magenta = tile was never decoded.
    const d = this._imageData.data;
    for (let i = 0; i < d.length; i += 4) {
      d[i] = 255;     // R
      d[i + 1] = 0;   // G
      d[i + 2] = 255;  // B
      d[i + 3] = 255;  // A
    }
    this.markDirty(0, 0, width, height);
  }

  get width(): number {
    return this._width;
  }

  get height(): number {
    return this._height;
  }

  get imageData(): ImageData {
    return this._imageData;
  }

  get pixels(): Uint8ClampedArray {
    return this._imageData.data;
  }

  get dirtyRects(): Array<{ x: number; y: number; w: number; h: number }> {
    return this._dirtyRects;
  }

  clearDirty(): void {
    this._dirtyRects = [];
  }

  markDirty(x: number, y: number, w: number, h: number): void {
    this._dirtyRects.push({ x, y, w, h });
  }

  resize(width: number, height: number): void {
    // Skip if dimensions unchanged — avoids clearing the framebuffer
    if (width === this._width && height === this._height) return;
    this._width = width;
    this._height = height;
    this._imageData = new ImageData(width, height);
    // Fill with magenta so un-decoded tiles are obvious
    const d = this._imageData.data;
    for (let i = 0; i < d.length; i += 4) {
      d[i] = 255; d[i + 1] = 0; d[i + 2] = 255; d[i + 3] = 255;
    }
    this._dirtyRects = [];
  }

  /**
   * Set a single pixel at (x, y) with RGBA values.
   */
  setPixel(x: number, y: number, r: number, g: number, b: number, a: number = 255): void {
    const offset = (y * this._width + x) * 4;
    this._imageData.data[offset] = r;
    this._imageData.data[offset + 1] = g;
    this._imageData.data[offset + 2] = b;
    this._imageData.data[offset + 3] = a;
  }

  /**
   * Write raw RGBA pixel data into a rectangular region.
   */
  writeRect(x: number, y: number, width: number, height: number, rgbaData: Uint8Array | Uint8ClampedArray): void {
    const dst = this._imageData.data;
    const fbWidth = this._width;

    for (let row = 0; row < height; row++) {
      const srcOffset = row * width * 4;
      const dstOffset = ((y + row) * fbWidth + x) * 4;
      dst.set(rgbaData.subarray(srcOffset, srcOffset + width * 4), dstOffset);
    }

    this.markDirty(x, y, width, height);
  }

  /**
   * Write 3-byte RGB pixel data directly into the framebuffer as RGBA.
   * Avoids an intermediate tile buffer copy for Tight basic encoding.
   */
  writeRectRGB(x: number, y: number, width: number, height: number, rgbData: Uint8Array): void {
    const dst = this._imageData.data;
    const fbWidth = this._width;

    for (let row = 0; row < height; row++) {
      const dstBase = ((y + row) * fbWidth + x) * 4;
      const srcBase = row * width * 3;
      for (let col = 0; col < width; col++) {
        const s = srcBase + col * 3;
        const d = dstBase + col * 4;
        dst[d] = rgbData[s];
        dst[d + 1] = rgbData[s + 1];
        dst[d + 2] = rgbData[s + 2];
        dst[d + 3] = 255;
      }
    }

    this.markDirty(x, y, width, height);
  }

  /**
   * Fill a rectangular region with a solid RGBA color.
   */
  fillRect(x: number, y: number, width: number, height: number, r: number, g: number, b: number): void {
    const dst = this._imageData.data;
    const fbWidth = this._width;
    // Pack RGBA into a 32-bit value for fast filling (little-endian: ABGR)
    const dst32 = new Uint32Array(dst.buffer, dst.byteOffset, dst.byteLength / 4);
    const rgba32 = (255 << 24) | (b << 16) | (g << 8) | r;

    for (let row = 0; row < height; row++) {
      const dstIdx = (y + row) * fbWidth + x;
      dst32.fill(rgba32, dstIdx, dstIdx + width);
    }

    this.markDirty(x, y, width, height);
  }

  /**
   * Copy a region within the framebuffer from (srcX, srcY) to (dstX, dstY).
   */
  copyRect(srcX: number, srcY: number, dstX: number, dstY: number, width: number, height: number): void {
    const data = this._imageData.data;
    const fbWidth = this._width;

    // Use a temp buffer to handle overlapping regions
    const temp = new Uint8ClampedArray(width * height * 4);
    for (let row = 0; row < height; row++) {
      const srcOffset = ((srcY + row) * fbWidth + srcX) * 4;
      temp.set(data.subarray(srcOffset, srcOffset + width * 4), row * width * 4);
    }

    for (let row = 0; row < height; row++) {
      const dstOffset = ((dstY + row) * fbWidth + dstX) * 4;
      data.set(temp.subarray(row * width * 4, (row + 1) * width * 4), dstOffset);
    }

    this.markDirty(dstX, dstY, width, height);
  }

  /**
   * Draw an ImageBitmap into the framebuffer at position (x, y).
   * Reuses a single OffscreenCanvas to avoid per-tile allocation overhead.
   */
  drawBitmap(bitmap: ImageBitmap, x: number, y: number, width: number, height: number): void {
    // Reuse or create the offscreen canvas, resizing only when needed
    if (!this._bitmapCanvas || this._bitmapCanvas.width < width || this._bitmapCanvas.height < height) {
      this._bitmapCanvas = new OffscreenCanvas(width, height);
      this._bitmapCtx = this._bitmapCanvas.getContext('2d', { willReadFrequently: true })!;
    }
    const ctx = this._bitmapCtx!;
    ctx.drawImage(bitmap, 0, 0);
    const imgData = ctx.getImageData(0, 0, width, height);
    this.writeRect(x, y, width, height, imgData.data);
  }
}
