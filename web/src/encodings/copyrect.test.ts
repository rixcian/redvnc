import { describe, it, expect } from 'vitest';
import { decodeCopyRect } from './copyrect';
import { Framebuffer } from '../framebuffer';
import type { RectHeader } from '../types';

describe('decodeCopyRect', () => {
  it('copies non-overlapping region', () => {
    const fb = new Framebuffer(10, 10);

    // Set red pixels at (0,0) - (1,1)
    fb.setPixel(0, 0, 255, 0, 0, 255);
    fb.setPixel(1, 0, 255, 0, 0, 255);
    fb.setPixel(0, 1, 255, 0, 0, 255);
    fb.setPixel(1, 1, 255, 0, 0, 255);

    // CopyRect: copy from (0,0) to (5,5), size 2x2
    const header: RectHeader = { x: 5, y: 5, width: 2, height: 2, encoding: 1 };
    const buf = new ArrayBuffer(4);
    const view = new DataView(buf);
    view.setUint16(0, 0); // srcX
    view.setUint16(2, 0); // srcY

    decodeCopyRect(fb, header, view);

    // Verify destination
    const offset = (5 * 10 + 5) * 4;
    expect(fb.pixels[offset]).toBe(255);     // R
    expect(fb.pixels[offset + 1]).toBe(0);   // G
    expect(fb.pixels[offset + 2]).toBe(0);   // B
    expect(fb.pixels[offset + 3]).toBe(255); // A
  });

  it('handles overlapping copy correctly', () => {
    const fb = new Framebuffer(10, 10);

    // Set distinct colors in a 3x1 strip: R, G, B
    fb.setPixel(0, 0, 255, 0, 0, 255);
    fb.setPixel(1, 0, 0, 255, 0, 255);
    fb.setPixel(2, 0, 0, 0, 255, 255);

    // Copy from (0,0) to (1,0), size 3x1 (overlapping)
    const header: RectHeader = { x: 1, y: 0, width: 3, height: 1, encoding: 1 };
    const buf = new ArrayBuffer(4);
    const view = new DataView(buf);
    view.setUint16(0, 0);
    view.setUint16(2, 0);

    decodeCopyRect(fb, header, view);

    // Destination should be: _, R, G, B (shifted right by 1)
    const p1 = (0 * 10 + 1) * 4;
    expect(fb.pixels[p1]).toBe(255); // R at position 1
    const p2 = (0 * 10 + 2) * 4;
    expect(fb.pixels[p2 + 1]).toBe(255); // G at position 2
    const p3 = (0 * 10 + 3) * 4;
    expect(fb.pixels[p3 + 2]).toBe(255); // B at position 3
  });
});
