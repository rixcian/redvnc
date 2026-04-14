import { describe, expect, it } from 'vitest';
import { pointerToFramebufferPixels } from './coordinate-map';

/** Minimal DOMRect-like object for tests */
function rect(x: number, y: number, w: number, h: number): DOMRect {
  return { x, y, width: w, height: h, top: y, left: x, right: x + w, bottom: y + h, toJSON: () => '' } as DOMRect;
}

describe('pointerToFramebufferPixels', () => {
  it('maps full rect when scaleToFit is false', () => {
    const r = rect(0, 0, 800, 600);
    expect(pointerToFramebufferPixels(100, 200, r, 1920, 1080, false)).toEqual({ x: 100, y: 200 });
  });

  it('matches object-fit contain: pillarbox (wide box, narrower content AR)', () => {
    // Layout 1000×600 (AR 5:3). FB 800×600 (AR 4:3). Content narrower than box → pillarbox L/R.
    const r = rect(0, 0, 1000, 600);
    const fbW = 800;
    const fbH = 600;
    const arContent = fbW / fbH;
    const drawH = 600;
    const drawW = drawH * arContent;
    const offX = (1000 - drawW) / 2;

    const centerY = 300;
    const rightEdgeFb = pointerToFramebufferPixels(
      offX + drawW - 1,
      centerY,
      r,
      fbW,
      fbH,
      true,
    );
    expect(rightEdgeFb.x).toBe(fbW - 1);

    const inRightMargin = pointerToFramebufferPixels(999, centerY, r, fbW, fbH, true);
    expect(inRightMargin.x).toBe(fbW - 1);
  });

  it('matches object-fit contain: letterbox (tall box, wider content AR)', () => {
    // Layout 800×1000. FB 1920×1080 (wider than box AR). Letterbox top/bottom.
    const r = rect(0, 0, 800, 1000);
    const fbW = 1920;
    const fbH = 1080;
    const drawW = 800;
    const drawH = drawW / (fbW / fbH);
    const offY = (1000 - drawH) / 2;

    const topFb = pointerToFramebufferPixels(400, offY, r, fbW, fbH, true);
    expect(topFb.y).toBe(0);
    const midFb = pointerToFramebufferPixels(400, offY + drawH / 2, r, fbW, fbH, true);
    expect(midFb.y).toBeGreaterThan(fbH / 2 - 2);
    expect(midFb.y).toBeLessThan(fbH / 2 + 2);
  });
});
