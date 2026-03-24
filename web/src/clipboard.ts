import { writeClipboardSet } from './rfb-writer';
import type { SendFn } from './input';

export class ClipboardHandler {
  private sendFn: SendFn;
  private clipboardCallback: ((text: string) => void) | null = null;
  private autoSync: boolean;
  private canvas: HTMLCanvasElement | null = null;
  private boundPaste: (e: ClipboardEvent) => void;

  constructor(sendFn: SendFn, autoSync: boolean = true) {
    this.sendFn = sendFn;
    this.autoSync = autoSync;
    this.boundPaste = this.handlePaste.bind(this);
  }

  /**
   * Attach to a canvas element to intercept paste events (Ctrl+V).
   * When the user pastes into the canvas, the browser clipboard text is
   * sent to the VNC server so the remote clipboard is up-to-date before
   * the Ctrl+V key events trigger the actual paste in the remote app.
   */
  attach(canvas: HTMLCanvasElement): void {
    this.canvas = canvas;
    canvas.addEventListener('paste', this.boundPaste);
  }

  detach(): void {
    if (this.canvas) {
      this.canvas.removeEventListener('paste', this.boundPaste);
      this.canvas = null;
    }
  }

  private handlePaste(e: ClipboardEvent): void {
    const text = e.clipboardData?.getData('text/plain');
    if (text) {
      this.sendClipboard(text);
    }
  }

  /**
   * Send clipboard text to the VNC server via the proxy.
   */
  sendClipboard(text: string): void {
    this.sendFn(writeClipboardSet(text));
  }

  /**
   * Handle clipboard update from the server.
   */
  handleClipboardUpdate(text: string): void {
    console.debug('[VNC] clipboard update from server', {
      textLen: text.length,
      preview: text.slice(0, 80),
      autoSync: this.autoSync,
    });

    this.clipboardCallback?.(text);

    if (this.autoSync) {
      if (!navigator.clipboard) {
        console.warn('[VNC] clipboard sync skipped: navigator.clipboard not available (requires HTTPS or localhost)');
        return;
      }
      // Try to write to the browser clipboard
      navigator.clipboard.writeText(text).then(() => {
        console.debug('[VNC] clipboard written to browser successfully');
      }).catch((err: unknown) => {
        console.warn('[VNC] clipboard write failed (requires document focus and permission)', err);
      });
    }
  }

  onClipboard(callback: (text: string) => void): void {
    this.clipboardCallback = callback;
  }
}
