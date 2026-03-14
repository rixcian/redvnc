import { writeClipboardSet } from './rfb-writer';
import type { SendFn } from './input';

export class ClipboardHandler {
  private sendFn: SendFn;
  private clipboardCallback: ((text: string) => void) | null = null;
  private autoSync: boolean;

  constructor(sendFn: SendFn, autoSync: boolean = true) {
    this.sendFn = sendFn;
    this.autoSync = autoSync;
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
    this.clipboardCallback?.(text);

    if (this.autoSync) {
      // Try to write to the browser clipboard
      navigator.clipboard?.writeText(text).catch(() => {
        // Clipboard API may fail without user interaction or permissions
      });
    }
  }

  onClipboard(callback: (text: string) => void): void {
    this.clipboardCallback = callback;
  }
}
