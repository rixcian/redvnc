import { writeClipboardSet } from './rfb-writer';
import type { SendFn } from './input';
import type { ClipboardEntry } from './types';

const MAX_HISTORY = 50;

/** Remote clipboard updates within this window after we send the same text are treated as echo (hide from history). */
const CLIPBOARD_ECHO_MS = 3000;

export class ClipboardHandler {
  private sendFn: SendFn;
  private clipboardCallback: ((text: string) => void) | null = null;
  private historyCallback: ((entries: ClipboardEntry[]) => void) | null = null;
  private _autoSync: boolean;
  private _history: ClipboardEntry[] = [];
  private _nextId = 1;
  private canvas: HTMLCanvasElement | null = null;
  private boundPaste: (e: ClipboardEvent) => void;
  /** Recent outbound texts (newest last); used to drop immediate echo-back into history. */
  private outboundEchoGuards: { text: string; sentAt: number }[] = [];

  constructor(sendFn: SendFn, autoSync: boolean = true) {
    this.sendFn = sendFn;
    this._autoSync = autoSync;
    this.boundPaste = this.handlePaste.bind(this);
  }

  get autoSync(): boolean {
    return this._autoSync;
  }

  set autoSync(enabled: boolean) {
    this._autoSync = enabled;
  }

  get history(): ClipboardEntry[] {
    return this._history;
  }

  /**
   * Attach to a canvas element to intercept paste events (Ctrl+V).
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
    this.addEntry(text, 'local');
    const now = Date.now();
    const cutoff = now - CLIPBOARD_ECHO_MS;
    this.outboundEchoGuards = this.outboundEchoGuards.filter((g) => g.sentAt >= cutoff);
    this.outboundEchoGuards.push({ text, sentAt: now });
  }

  /**
   * Handle clipboard update from the server.
   */
  handleClipboardUpdate(text: string): void {
    console.debug('[VNC] clipboard update from server', {
      textLen: text.length,
      preview: text.slice(0, 80),
      autoSync: this._autoSync,
    });

    const now = Date.now();
    const cutoff = now - CLIPBOARD_ECHO_MS;
    this.outboundEchoGuards = this.outboundEchoGuards.filter((g) => g.sentAt >= cutoff);

    let skipHistory = false;
    const guardIdx = this.outboundEchoGuards.findIndex(
      (g) => g.text === text && now - g.sentAt < CLIPBOARD_ECHO_MS,
    );
    if (guardIdx >= 0) {
      skipHistory = true;
      this.outboundEchoGuards.splice(guardIdx, 1);
    }

    if (!skipHistory) {
      this.addEntry(text, 'remote');
    }
    this.clipboardCallback?.(text);

    if (this._autoSync) {
      if (!navigator.clipboard) {
        console.warn('[VNC] clipboard sync skipped: navigator.clipboard not available (requires HTTPS or localhost)');
        return;
      }
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

  onHistoryChange(callback: (entries: ClipboardEntry[]) => void): void {
    this.historyCallback = callback;
  }

  private addEntry(text: string, source: 'local' | 'remote'): void {
    // Skip duplicate consecutive entries with the same text and source
    if (this._history.length > 0) {
      const last = this._history[0];
      if (last.text === text && last.source === source) return;
    }

    const entry: ClipboardEntry = {
      id: this._nextId++,
      text,
      source,
      timestamp: Date.now(),
    };
    this._history = [entry, ...this._history].slice(0, MAX_HISTORY);
    this.historyCallback?.(this._history);
  }
}
