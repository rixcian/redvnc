import { describe, it, expect } from 'vitest';
import { decodeRaw } from './raw';
import { Framebuffer } from '../framebuffer';
import type { RectHeader } from '../types';

describe('decodeRaw', () => {
  it('writes raw pixel data to framebuffer', () => {
    const fb = new Framebuffer(10, 10);
    const header: RectHeader = { x: 2, y: 3, width: 2, height: 2, encoding: 0 };

    // Create 2x2 of green pixels
    const pixels = new ArrayBuffer(2 * 2 * 4);
    const u8 = new Uint8Array(pixels);
    for (let i = 0; i < u8.length; i += 4) {
      u8[i] = 0;       // R
      u8[i + 1] = 255;  // G
      u8[i + 2] = 0;    // B
      u8[i + 3] = 255;  // A
    }

    fb.clearDirty();
    decodeRaw(fb, header, new DataView(pixels));

    // Check pixel at (2, 3) in framebuffer
    const offset = (3 * 10 + 2) * 4;
    expect(fb.pixels[offset]).toBe(0);      // R
    expect(fb.pixels[offset + 1]).toBe(255); // G
    expect(fb.pixels[offset + 2]).toBe(0);   // B
    expect(fb.pixels[offset + 3]).toBe(255); // A

    // Check pixel at (3, 4) — bottom-right of the 2x2 block
    const offset2 = (4 * 10 + 3) * 4;
    expect(fb.pixels[offset2 + 1]).toBe(255); // G

    // Verify dirty rect was tracked
    expect(fb.dirtyRects.length).toBe(1);
    expect(fb.dirtyRects[0]).toEqual({ x: 2, y: 3, w: 2, h: 2 });
  });
});
