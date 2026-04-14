/**
 * Map viewport pointer position to framebuffer pixel coordinates for a canvas.
 * When scaleToFit is true, matches CSS object-fit: contain (letterboxing /
 * pillarboxing inside the element's layout box).
 */
export function pointerToFramebufferPixels(
  clientX: number,
  clientY: number,
  canvasRect: DOMRectReadOnly,
  fbWidth: number,
  fbHeight: number,
  scaleToFit: boolean,
): { x: number; y: number } {
  const left = clientX - canvasRect.left;
  const top = clientY - canvasRect.top;

  if (!scaleToFit) {
    return {
      x: Math.floor(left),
      y: Math.floor(top),
    };
  }

  const W = canvasRect.width;
  const H = canvasRect.height;
  if (W <= 0 || H <= 0 || fbWidth <= 0 || fbHeight <= 0) {
    return { x: 0, y: 0 };
  }

  const arContent = fbWidth / fbHeight;
  const arBox = W / H;

  let drawW: number;
  let drawH: number;
  let offX: number;
  let offY: number;

  if (arContent > arBox) {
    drawW = W;
    drawH = W / arContent;
    offX = 0;
    offY = (H - drawH) / 2;
  } else {
    drawH = H;
    drawW = H * arContent;
    offX = (W - drawW) / 2;
    offY = 0;
  }

  if (drawW <= 0 || drawH <= 0) {
    return { x: 0, y: 0 };
  }

  let x = Math.floor(((left - offX) / drawW) * fbWidth);
  let y = Math.floor(((top - offY) / drawH) * fbHeight);

  x = Math.max(0, Math.min(fbWidth - 1, x));
  y = Math.max(0, Math.min(fbHeight - 1, y));
  return { x, y };
}
