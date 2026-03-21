import {
  ExtSessionInit,
  ExtClipboardUpdate,
  ExtUploadStatus,
  type SessionInitData,
} from './types';
import { tryGetMessageLength } from './rfb-parser';

export type MessageHandler = (type: number, data: DataView) => void;

export class VncConnection {
  private ws: WebSocket | null = null;
  private messageHandler: MessageHandler | null = null;
  private _connected = false;
  private buffer = new Uint8Array(0);

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
      this.buffer = new Uint8Array(0);

      let initResolved = false;

      ws.onopen = () => {
        this._connected = true;
      };

      ws.onmessage = (event: MessageEvent) => {
        const data = event.data as ArrayBuffer;
        if (data.byteLength < 1) return;

        const chunk = new Uint8Array(data);

        // Extension messages from the proxy are always sent as complete
        // WebSocket messages (they bypass the TCP relay). Detect and
        // dispatch them immediately to avoid mixing them into the
        // reassembly buffer for RFB stream data.
        if (this.buffer.length === 0 && chunk[0] >= 128 && chunk.length >= 5) {
          const peekView = new DataView(data);
          const payloadLen = peekView.getUint32(1);
          if (chunk.length === 5 + payloadLen) {
            // Matches extension message format exactly — dispatch directly
            if (!initResolved && chunk[0] === ExtSessionInit) {
              initResolved = true;
              resolve(parseSessionInit(peekView));
              return;
            }
            this.messageHandler?.(chunk[0], peekView);
            return;
          }
        }

        // Append to reassembly buffer
        const newBuf = new Uint8Array(this.buffer.length + chunk.length);
        newBuf.set(this.buffer);
        newBuf.set(chunk, this.buffer.length);
        this.buffer = newBuf;

        // Extract and dispatch complete messages
        while (this.buffer.length > 0) {
          // Check for extension messages at the head of the buffer
          // (can happen if an extension message landed in the buffer)
          if (this.buffer[0] >= 128 && this.buffer.length >= 5) {
            const extView = new DataView(this.buffer.buffer, this.buffer.byteOffset, this.buffer.byteLength);
            const payloadLen = extView.getUint32(1);
            const extMsgLen = 5 + payloadLen;
            if (this.buffer.length < extMsgLen) break;

            const msgBytes = this.buffer.slice(0, extMsgLen);
            this.buffer = this.buffer.slice(extMsgLen);
            const view = new DataView(msgBytes.buffer, msgBytes.byteOffset, msgBytes.byteLength);

            if (!initResolved && msgBytes[0] === ExtSessionInit) {
              initResolved = true;
              resolve(parseSessionInit(view));
              continue;
            }
            this.messageHandler?.(msgBytes[0], view);
            continue;
          }

          const msgLen = tryGetMessageLength(this.buffer);
          if (msgLen === -1 || msgLen > this.buffer.length) {
            break; // Need more data
          }

          const msgBytes = this.buffer.slice(0, msgLen);
          this.buffer = this.buffer.slice(msgLen);

          const view = new DataView(msgBytes.buffer, msgBytes.byteOffset, msgBytes.byteLength);
          const msgType = view.getUint8(0);

          this.messageHandler?.(msgType, view);
        }
      };

      ws.onerror = () => {
        if (!initResolved) {
          reject(new Error('WebSocket connection failed'));
        }
      };

      ws.onclose = (event: CloseEvent) => {
        this._connected = false;
        this.buffer = new Uint8Array(0);
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
      this.buffer = new Uint8Array(0);
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

  // authType is appended after name if available
  const authTypeOffset = 29 + nameLength;
  const authType = authTypeOffset < view.byteLength ? view.getUint8(authTypeOffset) : 0;

  return { width, height, pixelFormat: pf, name, authType };
}
