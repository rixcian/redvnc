import { describe, it, expect } from 'vitest';
import { Framebuffer } from './framebuffer';

describe('Framebuffer', () => {
  it('creates with correct dimensions', () => {
    const fb = new Framebuffer(800, 600);
    expect(fb.width).toBe(800);
    expect(fb.height).toBe(600);
    expect(fb.pixels.length).toBe(800 * 600 * 4);
  });

  it('sets a single pixel', () => {
    const fb = new Framebuffer(10, 10);
    fb.setPixel(5, 3, 255, 128, 0, 255);
    const offset = (3 * 10 + 5) * 4;
    expect(fb.pixels[offset]).toBe(255);     // R
    expect(fb.pixels[offset + 1]).toBe(128); // G
    expect(fb.pixels[offset + 2]).toBe(0);   // B
    expect(fb.pixels[offset + 3]).toBe(255); // A
  });

  it('writes a rectangular region', () => {
    const fb = new Framebuffer(10, 10);
    const rect = new Uint8Array(2 * 2 * 4); // 2x2 pixels
    // Fill with red
    for (let i = 0; i < rect.length; i += 4) {
      rect[i] = 255;     // R
      rect[i + 1] = 0;   // G
      rect[i + 2] = 0;   // B
      rect[i + 3] = 255; // A
    }
    fb.writeRect(1, 1, 2, 2, rect);

    // Check pixel at (1, 1)
    const offset = (1 * 10 + 1) * 4;
    expect(fb.pixels[offset]).toBe(255);
    expect(fb.pixels[offset + 1]).toBe(0);

    // Check pixel at (0, 0) — should be unchanged (black/transparent)
    expect(fb.pixels[0]).toBe(0);
  });

  it('tracks dirty rectangles', () => {
    const fb = new Framebuffer(100, 100);
    expect(fb.dirtyRects.length).toBe(0);

    fb.markDirty(10, 20, 30, 40);
    expect(fb.dirtyRects.length).toBe(1);
    expect(fb.dirtyRects[0]).toEqual({ x: 10, y: 20, w: 30, h: 40 });

    fb.clearDirty();
    expect(fb.dirtyRects.length).toBe(0);
  });

  it('resizes the framebuffer', () => {
    const fb = new Framebuffer(100, 100);
    fb.setPixel(0, 0, 255, 0, 0);
    fb.markDirty(0, 0, 100, 100);

    fb.resize(200, 150);
    expect(fb.width).toBe(200);
    expect(fb.height).toBe(150);
    expect(fb.pixels.length).toBe(200 * 150 * 4);
    expect(fb.dirtyRects.length).toBe(0);
    // Pixel data should be zeroed after resize
    expect(fb.pixels[0]).toBe(0);
  });

  it('copies a region within the framebuffer', () => {
    const fb = new Framebuffer(10, 10);
    // Set a 2x2 red block at (0, 0)
    fb.setPixel(0, 0, 255, 0, 0, 255);
    fb.setPixel(1, 0, 255, 0, 0, 255);
    fb.setPixel(0, 1, 255, 0, 0, 255);
    fb.setPixel(1, 1, 255, 0, 0, 255);

    // Copy to (5, 5)
    fb.copyRect(0, 0, 5, 5, 2, 2);

    const offset = (5 * 10 + 5) * 4;
    expect(fb.pixels[offset]).toBe(255); // R at (5,5)
    expect(fb.pixels[offset + 1]).toBe(0);
    expect(fb.pixels[offset + 2]).toBe(0);
  });
});
