import { VncConnection } from './connection';
import { parseServerMessage } from './rfb-parser';
import type { UploadStatusMessage } from './rfb-parser';
import {
  writeSetPixelFormat,
  writeSetEncodings,
  writeFramebufferUpdateRequest,
} from './rfb-writer';
import { Framebuffer } from './framebuffer';
import { CanvasRenderer } from './renderer';
import { InputHandler } from './input';
import { ClipboardHandler } from './clipboard';
import { FileUploadHandler } from './file-upload';
import { decodeRaw } from './encodings/raw';
import { decodeCopyRect } from './encodings/copyrect';
import { ZlibDecoder } from './encodings/zlib';
import { TightDecoder } from './encodings/tight';
import { ZrleDecoder } from './encodings/zrle';
import {
  MsgFramebufferUpdate,
  MsgBell,
  MsgServerCutText,
  ExtClipboardUpdate,
  ExtUploadStatus,
  EncodingRaw,
  EncodingCopyRect,
  EncodingZlib,
  EncodingTight,
  EncodingZRLE,
  EncodingCursor,
  EncodingDesktopSize,
  RGBA_PIXEL_FORMAT,
  DEFAULT_ENCODINGS,
  type VncClientOptions,
  type VncEventMap,
  type ConnectionStats,
  type UploadOptions,
  type UploadResult,
  type UploadProgress,
} from './types';

export type { VncClientOptions, UploadOptions, UploadResult, UploadProgress, ConnectionStats };
export type { VncViewerProps } from './types';

const ENCODING_NAMES: Record<number, string> = {
  [EncodingRaw]: 'Raw',
  [EncodingCopyRect]: 'CopyRect',
  [EncodingZlib]: 'Zlib',
  [EncodingTight]: 'Tight',
  [EncodingZRLE]: 'ZRLE',
  [EncodingCursor]: 'Cursor',
  [EncodingDesktopSize]: 'DesktopSize',
};

const AUTH_TYPE_NAMES: Record<number, string> = {
  0: 'Unknown',
  1: 'None',
  2: 'VNC Password',
};

export class VncClient {
  private options: VncClientOptions;
  private connection: VncConnection;
  private framebuffer: Framebuffer | null = null;
  private renderer: CanvasRenderer;
  private inputHandler: InputHandler;
  private clipboardHandler: ClipboardHandler;
  private fileUploadHandler: FileUploadHandler;
  private zlibDecoder: ZlibDecoder;
  private tightDecoder: TightDecoder;
  private zrleDecoder: ZrleDecoder;

  private _connected = false;
  private _width = 0;
  private _height = 0;
  private _name = '';
  private _authType = 0;

  // Stats tracking
  private _encodingCounts: Record<number, number> = {};
  private _totalRectangles = 0;
  private _bytesReceived = 0;
  private _fbuTimestamps: number[] = []; // timestamps of recent FramebufferUpdate messages

  private eventHandlers: { [K in keyof VncEventMap]?: VncEventMap[K][] } = {};
  private rafId: number | null = null;
  private intentionalDisconnect = false;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private reconnectAttempt = 0;
  private canvas: HTMLCanvasElement | null = null;

  constructor(options: VncClientOptions) {
    this.options = options;
    this.connection = new VncConnection();
    this.renderer = new CanvasRenderer(options.scaleToFit ?? false);
    this.zlibDecoder = new ZlibDecoder();
    this.tightDecoder = new TightDecoder();
    this.zrleDecoder = new ZrleDecoder();

    const sendFn = (data: ArrayBuffer | Uint8Array) => this.connection.send(data);
    this.inputHandler = new InputHandler(this.renderer, sendFn, options.viewOnly ?? false);
    this.clipboardHandler = new ClipboardHandler(sendFn, options.clipboardSync ?? true);
    this.fileUploadHandler = new FileUploadHandler(sendFn, options.uploadDir ?? '');
  }

  get connected(): boolean {
    return this._connected;
  }

  get width(): number {
    return this._width;
  }

  get height(): number {
    return this._height;
  }

  get name(): string {
    return this._name;
  }

  getStats(): ConnectionStats {
    const now = performance.now();
    // Count FBU timestamps within the last 1 second
    const cutoff = now - 1000;
    this._fbuTimestamps = this._fbuTimestamps.filter(t => t > cutoff);
    const fps = this._fbuTimestamps.length;

    const encodings: Record<string, number> = {};
    for (const [enc, count] of Object.entries(this._encodingCounts)) {
      const name = ENCODING_NAMES[Number(enc)] ?? `Unknown(${enc})`;
      encodings[name] = count;
    }

    return {
      serverName: this._name,
      resolution: { width: this._width, height: this._height },
      authType: AUTH_TYPE_NAMES[this._authType] ?? `Type ${this._authType}`,
      encodings,
      fps,
      totalRectangles: this._totalRectangles,
      bytesReceived: this._bytesReceived,
    };
  }

  private resetStats(): void {
    this._encodingCounts = {};
    this._totalRectangles = 0;
    this._bytesReceived = 0;
    this._fbuTimestamps = [];
  }

  async connect(): Promise<void> {
    // Initialize decoders
    await Promise.all([
      this.zlibDecoder.init(),
      this.tightDecoder.init(),
      this.zrleDecoder.init(),
    ]);

    // Set up message handler
    this.connection.onMessage((type, view) => {
      this.handleMessage(type, view);
    });

    // Connect and receive SessionInit
    const sessionInit = await this.connection.connect(
      this.options.url,
      this.options.target,
      this.options.password,
    );

    this._width = sessionInit.width;
    this._height = sessionInit.height;
    this._name = sessionInit.name;
    this._authType = sessionInit.authType;
    this._connected = true;
    this.resetStats();

    // Initialize framebuffer
    this.framebuffer = new Framebuffer(this._width, this._height);
    this.inputHandler.setFramebufferSize(this._width, this._height);
    this.renderer.updateCanvasSize(this.framebuffer);

    // Send SetPixelFormat (request RGBA for direct ImageData compatibility)
    this.connection.send(writeSetPixelFormat(RGBA_PIXEL_FORMAT));

    // Send SetEncodings
    const encodings = this.options.encodings ?? DEFAULT_ENCODINGS;
    this.connection.send(writeSetEncodings(encodings));

    // Request initial full framebuffer update
    this.connection.send(
      writeFramebufferUpdateRequest(false, 0, 0, this._width, this._height),
    );

    // Start render loop
    this.startRenderLoop();

    this.emit('connect');
  }

  disconnect(): void {
    this.intentionalDisconnect = true;
    this.cancelReconnect();
    this.stopRenderLoop();
    this.connection.disconnect();
    this.inputHandler.detach();
    this.renderer.detach();
    this.fileUploadHandler.cleanup();
    this._connected = false;
  }

  attachCanvas(canvas: HTMLCanvasElement): void {
    this.canvas = canvas;
    this.renderer.attach(canvas);
    this.inputHandler.attach(canvas);
    if (this.framebuffer) {
      this.renderer.updateCanvasSize(this.framebuffer);
    }
  }

  detachCanvas(): void {
    this.inputHandler.detach();
    this.renderer.detach();
  }

  sendClipboard(text: string): void {
    this.clipboardHandler.sendClipboard(text);
  }

  onClipboard(callback: (text: string) => void): void {
    this.clipboardHandler.onClipboard(callback);
  }

  async uploadFile(file: File, options?: UploadOptions): Promise<UploadResult> {
    return this.fileUploadHandler.uploadFile(file, options);
  }

  onUploadProgress(callback: (progress: UploadProgress) => void): void {
    this.fileUploadHandler.onUploadProgress(callback);
  }

  setUploadDir(path: string): void {
    this.fileUploadHandler.setUploadDir(path);
  }

  on<K extends keyof VncEventMap>(event: K, cb: VncEventMap[K]): void {
    if (!this.eventHandlers[event]) {
      this.eventHandlers[event] = [];
    }
    this.eventHandlers[event]!.push(cb);
  }

  private emit<K extends keyof VncEventMap>(event: K, ...args: Parameters<VncEventMap[K]>): void {
    const handlers = this.eventHandlers[event];
    if (handlers) {
      for (const handler of handlers) {
        (handler as (...a: unknown[]) => void)(...args);
      }
    }
  }

  private handleMessage(type: number, view: DataView): void {
    this._bytesReceived += view.byteLength;

    let msg;
    try {
      msg = parseServerMessage(type, view);
    } catch (err) {
      console.warn('RFB parse error (msgType=%d, %d bytes):', type, view.byteLength, err);
      return;
    }
    if (!msg) return;

    switch (msg.type) {
      case MsgFramebufferUpdate:
        this.handleFramebufferUpdate(msg);
        break;
      case MsgBell:
        this.emit('bell');
        break;
      case MsgServerCutText:
        this.clipboardHandler.handleClipboardUpdate(msg.text);
        this.emit('clipboard', msg.text);
        break;
      case ExtClipboardUpdate:
        this.clipboardHandler.handleClipboardUpdate(msg.text);
        this.emit('clipboard', msg.text);
        break;
      case ExtUploadStatus: {
        const us = msg as UploadStatusMessage;
        this.fileUploadHandler.handleUploadStatus(
          us.uploadId,
          us.status,
          us.bytesWritten,
          us.messageText,
        );
        break;
      }
      case -1: // disconnect
        this._connected = false;
        this.stopRenderLoop();
        this.fileUploadHandler.cleanup();
        this.emit('disconnect', 'connection closed');
        this.attemptReconnect();
        break;
    }
  }

  private async handleFramebufferUpdate(
    msg: import('./rfb-parser').FramebufferUpdateMessage,
  ): Promise<void> {
    if (!this.framebuffer) return;

    this._fbuTimestamps.push(performance.now());

    const asyncTasks: Promise<void>[] = [];

    for (const rect of msg.rectangles) {
      const { header, data } = rect;
      this._encodingCounts[header.encoding] = (this._encodingCounts[header.encoding] ?? 0) + 1;
      this._totalRectangles++;

      switch (header.encoding) {
        case EncodingRaw:
          decodeRaw(this.framebuffer, header, data);
          break;
        case EncodingCopyRect:
          decodeCopyRect(this.framebuffer, header, data);
          break;
        case EncodingZlib:
          this.zlibDecoder.decode(this.framebuffer, header, data);
          break;
        case EncodingTight:
          // Tight may be async (JPEG decode). Collect the promise so we
          // wait for all tiles to finish before requesting the next update.
          asyncTasks.push(this.tightDecoder.decode(this.framebuffer, header, data));
          break;
        case EncodingZRLE:
          this.zrleDecoder.decode(this.framebuffer, header, data);
          break;
        case EncodingCursor:
          this.handleCursor(header, data);
          break;
        case EncodingDesktopSize:
          this.handleDesktopResize(header);
          break;
      }
    }

    // Wait for any async decoders (e.g. Tight JPEG) to finish before
    // requesting the next update. This prevents the server from sending
    // new rectangles that overwrite tiles still being decoded, which
    // causes flickering and incomplete rendering at the bottom of the screen.
    if (asyncTasks.length > 0) {
      await Promise.all(asyncTasks);
    }

    // Request next incremental update
    this.connection.send(
      writeFramebufferUpdateRequest(true, 0, 0, this._width, this._height),
    );
  }

  private handleCursor(
    header: import('./types').RectHeader,
    data: DataView,
  ): void {
    const { x: hotX, y: hotY, width, height } = header;
    if (width === 0 || height === 0) return;

    const pixelBytes = width * height * 4;
    const pixels = new Uint8Array(data.buffer, data.byteOffset, pixelBytes);
    const maskRowBytes = Math.ceil(width / 8);
    const mask = new Uint8Array(data.buffer, data.byteOffset + pixelBytes, maskRowBytes * height);

    // Build RGBA cursor image applying the bitmask
    const cursorImage = new Uint8Array(width * height * 4);
    for (let y = 0; y < height; y++) {
      for (let x = 0; x < width; x++) {
        const pixelIdx = (y * width + x) * 4;
        const maskByte = mask[y * maskRowBytes + Math.floor(x / 8)];
        const maskBit = (maskByte >> (7 - (x % 8))) & 1;

        cursorImage[pixelIdx] = pixels[pixelIdx];
        cursorImage[pixelIdx + 1] = pixels[pixelIdx + 1];
        cursorImage[pixelIdx + 2] = pixels[pixelIdx + 2];
        cursorImage[pixelIdx + 3] = maskBit ? 255 : 0;
      }
    }

    this.renderer.setCursor(cursorImage, width, height, hotX, hotY);
  }

  private handleDesktopResize(header: import('./types').RectHeader): void {
    this._width = header.width;
    this._height = header.height;
    this.framebuffer?.resize(this._width, this._height);
    this.inputHandler.setFramebufferSize(this._width, this._height);
    if (this.framebuffer) {
      this.renderer.updateCanvasSize(this.framebuffer);
    }
    this.emit('resize', this._width, this._height);

    // Request full framebuffer update after resize
    this.connection.send(
      writeFramebufferUpdateRequest(false, 0, 0, this._width, this._height),
    );
  }

  private attemptReconnect(): void {
    if (this.intentionalDisconnect) return;
    if (this.options.reconnect === false) return;

    const maxAttempts = this.options.maxReconnectAttempts ?? 10;
    if (this.reconnectAttempt >= maxAttempts) {
      this.emit('reconnect_failed');
      return;
    }

    this.reconnectAttempt++;
    const baseDelay = this.options.reconnectBaseDelay ?? 1000;
    const maxDelay = this.options.reconnectMaxDelay ?? 30000;
    const delay = Math.min(baseDelay * Math.pow(2, this.reconnectAttempt - 1), maxDelay);
    // Add jitter (+-20%)
    const jitter = delay * (0.8 + Math.random() * 0.4);

    this.emit('reconnecting', this.reconnectAttempt);

    this.reconnectTimer = setTimeout(async () => {
      try {
        // Create fresh connection
        this.connection = new VncConnection();
        await this.performReconnect();
        this.reconnectAttempt = 0;
        this.emit('reconnected');
      } catch {
        this.attemptReconnect();
      }
    }, jitter);
  }

  private async performReconnect(): Promise<void> {
    // Re-init decoders
    await Promise.all([
      this.zlibDecoder.init(),
      this.tightDecoder.init(),
      this.zrleDecoder.init(),
    ]);

    // Re-wire send function for handlers
    const sendFn = (data: ArrayBuffer | Uint8Array) => this.connection.send(data);
    this.inputHandler = new InputHandler(this.renderer, sendFn, this.options.viewOnly ?? false);
    this.clipboardHandler = new ClipboardHandler(sendFn, this.options.clipboardSync ?? true);
    this.fileUploadHandler = new FileUploadHandler(sendFn, this.options.uploadDir ?? '');

    this.connection.onMessage((type, view) => {
      this.handleMessage(type, view);
    });

    const sessionInit = await this.connection.connect(
      this.options.url,
      this.options.target,
      this.options.password,
    );

    this._width = sessionInit.width;
    this._height = sessionInit.height;
    this._name = sessionInit.name;
    this._authType = sessionInit.authType;
    this._connected = true;
    this.resetStats();

    this.framebuffer = new Framebuffer(this._width, this._height);
    this.inputHandler.setFramebufferSize(this._width, this._height);
    this.renderer.updateCanvasSize(this.framebuffer);

    // Re-send pixel format and encodings
    this.connection.send(writeSetPixelFormat(RGBA_PIXEL_FORMAT));
    const encodings = this.options.encodings ?? DEFAULT_ENCODINGS;
    this.connection.send(writeSetEncodings(encodings));

    // Request full framebuffer update
    this.connection.send(
      writeFramebufferUpdateRequest(false, 0, 0, this._width, this._height),
    );

    // Re-attach canvas if we had one
    if (this.canvas) {
      this.inputHandler.attach(this.canvas);
    }

    this.startRenderLoop();
  }

  private cancelReconnect(): void {
    if (this.reconnectTimer !== null) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    this.reconnectAttempt = 0;
  }

  private startRenderLoop(): void {
    const loop = () => {
      if (this.framebuffer) {
        this.renderer.render(this.framebuffer);
      }
      this.rafId = requestAnimationFrame(loop);
    };
    this.rafId = requestAnimationFrame(loop);
  }

  private stopRenderLoop(): void {
    if (this.rafId !== null) {
      cancelAnimationFrame(this.rafId);
      this.rafId = null;
    }
  }
}

// Re-export React components
export { VncViewer } from './components/VncViewer';
export { DebugOverlay } from './components/DebugOverlay';
export { FileUploadDropZone } from './components/FileUpload';
export { Toolbar } from './components/Toolbar';
