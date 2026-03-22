import { writeKeyEvent, writePointerEvent } from './rfb-writer';
import type { IRenderer } from './renderer-interface';

// X11 keysym mapping from KeyboardEvent.code
const CODE_TO_KEYSYM: Record<string, number> = {
  Backspace: 0xff08,
  Tab: 0xff09,
  Enter: 0xff0d,
  Escape: 0xff1b,
  Delete: 0xffff,
  Home: 0xff50,
  End: 0xff57,
  PageUp: 0xff55,
  PageDown: 0xff56,
  ArrowLeft: 0xff51,
  ArrowUp: 0xff52,
  ArrowRight: 0xff53,
  ArrowDown: 0xff54,
  Insert: 0xff63,
  F1: 0xffbe,
  F2: 0xffbf,
  F3: 0xffc0,
  F4: 0xffc1,
  F5: 0xffc2,
  F6: 0xffc3,
  F7: 0xffc4,
  F8: 0xffc5,
  F9: 0xffc6,
  F10: 0xffc7,
  F11: 0xffc8,
  F12: 0xffc9,
  ShiftLeft: 0xffe1,
  ShiftRight: 0xffe2,
  ControlLeft: 0xffe3,
  ControlRight: 0xffe4,
  AltLeft: 0xffe9,
  AltRight: 0xffea,
  MetaLeft: 0xffe7,
  MetaRight: 0xffe8,
  CapsLock: 0xffe5,
  NumLock: 0xff7f,
  ScrollLock: 0xff14,
  Pause: 0xff13,
  PrintScreen: 0xff61,
  Space: 0x0020,
};

// Map key names that are single characters (a-z, 0-9, etc.)
const KEY_TO_KEYSYM: Record<string, number> = {
  ' ': 0x0020,
};

/**
 * Convert a KeyboardEvent to an X11 keysym.
 */
function keyEventToKeysym(event: KeyboardEvent): number {
  // Check code-based mapping first (for modifier/special keys)
  const codeSym = CODE_TO_KEYSYM[event.code];
  if (codeSym !== undefined) return codeSym;

  // Check key name mapping
  const keySym = KEY_TO_KEYSYM[event.key];
  if (keySym !== undefined) return keySym;

  // For single printable characters, use the char code directly
  if (event.key.length === 1) {
    const code = event.key.charCodeAt(0);
    // Standard Latin-1 maps directly to X11 keysyms
    if (code <= 0xff) return code;
    // Unicode characters above U+00FF use keysym = 0x01000000 + codepoint
    return 0x01000000 + code;
  }

  return 0;
}

export type SendFn = (data: ArrayBuffer) => void;

export class InputHandler {
  private canvas: HTMLCanvasElement | null = null;
  private renderer: IRenderer;
  private sendFn: SendFn;
  private fbWidth: number = 0;
  private fbHeight: number = 0;
  private buttonMask: number = 0;
  private viewOnly: boolean;

  // Bound event handlers for cleanup
  private boundKeyDown: (e: KeyboardEvent) => void;
  private boundKeyUp: (e: KeyboardEvent) => void;
  private boundMouseMove: (e: MouseEvent) => void;
  private boundMouseDown: (e: MouseEvent) => void;
  private boundMouseUp: (e: MouseEvent) => void;
  private boundWheel: (e: WheelEvent) => void;
  private boundContextMenu: (e: Event) => void;
  private boundTouchStart: (e: TouchEvent) => void;
  private boundTouchMove: (e: TouchEvent) => void;
  private boundTouchEnd: (e: TouchEvent) => void;

  constructor(renderer: IRenderer, sendFn: SendFn, viewOnly: boolean = false) {
    this.renderer = renderer;
    this.sendFn = sendFn;
    this.viewOnly = viewOnly;

    this.boundKeyDown = this.handleKeyDown.bind(this);
    this.boundKeyUp = this.handleKeyUp.bind(this);
    this.boundMouseMove = this.handleMouseMove.bind(this);
    this.boundMouseDown = this.handleMouseDown.bind(this);
    this.boundMouseUp = this.handleMouseUp.bind(this);
    this.boundWheel = this.handleWheel.bind(this);
    this.boundContextMenu = (e: Event) => e.preventDefault();
    this.boundTouchStart = this.handleTouchStart.bind(this);
    this.boundTouchMove = this.handleTouchMove.bind(this);
    this.boundTouchEnd = this.handleTouchEnd.bind(this);
  }

  setFramebufferSize(width: number, height: number): void {
    this.fbWidth = width;
    this.fbHeight = height;
  }

  attach(canvas: HTMLCanvasElement): void {
    this.canvas = canvas;
    canvas.tabIndex = 0; // Make canvas focusable

    if (this.viewOnly) return;

    canvas.addEventListener('keydown', this.boundKeyDown);
    canvas.addEventListener('keyup', this.boundKeyUp);
    canvas.addEventListener('mousemove', this.boundMouseMove);
    canvas.addEventListener('mousedown', this.boundMouseDown);
    canvas.addEventListener('mouseup', this.boundMouseUp);
    canvas.addEventListener('wheel', this.boundWheel, { passive: false });
    canvas.addEventListener('contextmenu', this.boundContextMenu);
    canvas.addEventListener('touchstart', this.boundTouchStart, { passive: false });
    canvas.addEventListener('touchmove', this.boundTouchMove, { passive: false });
    canvas.addEventListener('touchend', this.boundTouchEnd, { passive: false });
  }

  detach(): void {
    if (!this.canvas) return;
    const canvas = this.canvas;

    canvas.removeEventListener('keydown', this.boundKeyDown);
    canvas.removeEventListener('keyup', this.boundKeyUp);
    canvas.removeEventListener('mousemove', this.boundMouseMove);
    canvas.removeEventListener('mousedown', this.boundMouseDown);
    canvas.removeEventListener('mouseup', this.boundMouseUp);
    canvas.removeEventListener('wheel', this.boundWheel);
    canvas.removeEventListener('contextmenu', this.boundContextMenu);
    canvas.removeEventListener('touchstart', this.boundTouchStart);
    canvas.removeEventListener('touchmove', this.boundTouchMove);
    canvas.removeEventListener('touchend', this.boundTouchEnd);

    this.canvas = null;
  }

  private handleKeyDown(e: KeyboardEvent): void {
    e.preventDefault();
    const keysym = keyEventToKeysym(e);
    if (keysym) {
      this.sendFn(writeKeyEvent(true, keysym));
    }
  }

  private handleKeyUp(e: KeyboardEvent): void {
    e.preventDefault();
    const keysym = keyEventToKeysym(e);
    if (keysym) {
      this.sendFn(writeKeyEvent(false, keysym));
    }
  }

  private handleMouseMove(e: MouseEvent): void {
    const { x, y } = this.renderer.translateCoordinates(e, this.fbWidth, this.fbHeight);
    this.sendFn(writePointerEvent(this.buttonMask, x, y));
  }

  private handleMouseDown(e: MouseEvent): void {
    this.canvas?.focus();
    const buttonBit = mouseButtonToBit(e.button);
    this.buttonMask |= buttonBit;
    const { x, y } = this.renderer.translateCoordinates(e, this.fbWidth, this.fbHeight);
    this.sendFn(writePointerEvent(this.buttonMask, x, y));
  }

  private handleMouseUp(e: MouseEvent): void {
    const buttonBit = mouseButtonToBit(e.button);
    this.buttonMask &= ~buttonBit;
    const { x, y } = this.renderer.translateCoordinates(e, this.fbWidth, this.fbHeight);
    this.sendFn(writePointerEvent(this.buttonMask, x, y));
  }

  private handleWheel(e: WheelEvent): void {
    e.preventDefault();
    const { x, y } = this.renderer.translateCoordinates(e, this.fbWidth, this.fbHeight);

    // Scroll up = button 4, scroll down = button 5
    const scrollButton = e.deltaY < 0 ? (1 << 3) : (1 << 4);

    // Press and release the scroll button
    this.sendFn(writePointerEvent(this.buttonMask | scrollButton, x, y));
    this.sendFn(writePointerEvent(this.buttonMask, x, y));
  }

  private handleTouchStart(e: TouchEvent): void {
    e.preventDefault();
    if (e.touches.length === 1) {
      const touch = e.touches[0];
      const { x, y } = this.touchToVncCoords(touch);
      this.buttonMask = 1; // Left button
      this.sendFn(writePointerEvent(this.buttonMask, x, y));
    }
  }

  private handleTouchMove(e: TouchEvent): void {
    e.preventDefault();
    if (e.touches.length === 1) {
      const touch = e.touches[0];
      const { x, y } = this.touchToVncCoords(touch);
      this.sendFn(writePointerEvent(this.buttonMask, x, y));
    }
  }

  private handleTouchEnd(e: TouchEvent): void {
    e.preventDefault();
    if (e.changedTouches.length === 1) {
      const touch = e.changedTouches[0];
      const { x, y } = this.touchToVncCoords(touch);
      this.buttonMask = 0;
      this.sendFn(writePointerEvent(this.buttonMask, x, y));
    }
  }

  private touchToVncCoords(touch: Touch): { x: number; y: number } {
    if (!this.canvas) return { x: 0, y: 0 };
    const rect = this.canvas.getBoundingClientRect();
    const canvasX = touch.clientX - rect.left;
    const canvasY = touch.clientY - rect.top;
    const scaleX = this.fbWidth / rect.width;
    const scaleY = this.fbHeight / rect.height;
    return {
      x: Math.floor(canvasX * scaleX),
      y: Math.floor(canvasY * scaleY),
    };
  }
}

function mouseButtonToBit(button: number): number {
  switch (button) {
    case 0: return 1;       // Left
    case 1: return 1 << 1;  // Middle
    case 2: return 1 << 2;  // Right
    default: return 0;
  }
}
