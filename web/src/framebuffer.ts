export class Framebuffer {
  private _width: number;
  private _height: number;
  private _imageData: ImageData;
  private _dirtyRects: Array<{ x: number; y: number; w: number; h: number }> = [];

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
   * Uses an offscreen canvas to extract pixel data.
   */
  drawBitmap(bitmap: ImageBitmap, x: number, y: number, width: number, height: number): void {
    const canvas = new OffscreenCanvas(width, height);
    const ctx = canvas.getContext('2d')!;
    ctx.drawImage(bitmap, 0, 0);
    const imgData = ctx.getImageData(0, 0, width, height);
    this.writeRect(x, y, width, height, imgData.data);
  }
}
