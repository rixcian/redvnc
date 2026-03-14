import {
  ExtSessionInit,
  ExtClipboardUpdate,
  ExtUploadStatus,
  type SessionInitData,
} from './types';

export type MessageHandler = (type: number, data: DataView) => void;

export class VncConnection {
  private ws: WebSocket | null = null;
  private messageHandler: MessageHandler | null = null;
  private _connected = false;

  get connected(): boolean {
    return this._connected;
  }

  onMessage(handler: MessageHandler): void {
    this.messageHandler = handler;
  }

  connect(url: string, target: string, password?: string): Promise<SessionInitData> {
    return new Promise((resolve, reject) => {
      const wsUrl = new URL(url);
      wsUrl.searchParams.set('target', target);
      if (password) {
        wsUrl.searchParams.set('password', password);
      }

      const ws = new WebSocket(wsUrl.toString(), 'binary');
      ws.binaryType = 'arraybuffer';
      this.ws = ws;

      let initResolved = false;

      ws.onopen = () => {
        this._connected = true;
      };

      ws.onmessage = (event: MessageEvent) => {
        const data = event.data as ArrayBuffer;
        if (data.byteLength < 1) return;

        const view = new DataView(data);
        const msgType = view.getUint8(0);

        // Before session init is resolved, only handle ExtSessionInit
        if (!initResolved && msgType === ExtSessionInit) {
          initResolved = true;
          const sessionInit = parseSessionInit(view);
          resolve(sessionInit);
          return;
        }

        // Extension messages from server
        if (msgType >= 128) {
          this.messageHandler?.(msgType, view);
          return;
        }

        // Standard RFB server messages
        this.messageHandler?.(msgType, view);
      };

      ws.onerror = () => {
        if (!initResolved) {
          reject(new Error('WebSocket connection failed'));
        }
      };

      ws.onclose = (event: CloseEvent) => {
        this._connected = false;
        if (!initResolved) {
          reject(new Error(`WebSocket closed: ${event.reason || 'unknown'}`));
        }
        // The disconnect event is handled by the VncClient via the message handler
        this.messageHandler?.(-1, new DataView(new ArrayBuffer(0)));
      };
    });
  }

  send(data: ArrayBuffer | Uint8Array): void {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(data);
    }
  }

  disconnect(): void {
    if (this.ws) {
      this.ws.close();
      this.ws = null;
      this._connected = false;
    }
  }
}

function parseSessionInit(view: DataView): SessionInitData {
  // type(1) + length(4) + payload
  const width = view.getUint16(5);
  const height = view.getUint16(7);

  // PixelFormat starts at offset 9, 16 bytes
  const pf = {
    bitsPerPixel: view.getUint8(9),
    depth: view.getUint8(10),
    bigEndian: view.getUint8(11) !== 0,
    trueColour: view.getUint8(12) !== 0,
    redMax: view.getUint16(13),
    greenMax: view.getUint16(15),
    blueMax: view.getUint16(17),
    redShift: view.getUint8(19),
    greenShift: view.getUint8(20),
    blueShift: view.getUint8(21),
    // 3 bytes padding at 22-24
  };

  const nameLength = view.getUint32(25);
  const nameBytes = new Uint8Array(view.buffer, view.byteOffset + 29, nameLength);
  const name = new TextDecoder().decode(nameBytes);

  return { width, height, pixelFormat: pf, name };
}
