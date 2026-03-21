// Polyfill ImageData for Node.js test environment
if (typeof globalThis.ImageData === 'undefined') {
  class ImageDataPolyfill {
    readonly width: number;
    readonly height: number;
    readonly data: Uint8ClampedArray;

    constructor(width: number, height: number);
    constructor(data: Uint8ClampedArray, width: number, height?: number);
    constructor(widthOrData: number | Uint8ClampedArray, widthOrHeight: number, height?: number) {
      if (widthOrData instanceof Uint8ClampedArray) {
        this.data = widthOrData;
        this.width = widthOrHeight;
        this.height = height ?? (widthOrData.length / (widthOrHeight * 4));
      } else {
        this.width = widthOrData;
        this.height = widthOrHeight;
        this.data = new Uint8ClampedArray(this.width * this.height * 4);
      }
    }
  }

  (globalThis as Record<string, unknown>).ImageData = ImageDataPolyfill;
}
