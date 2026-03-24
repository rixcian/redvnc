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
  // Pre-allocated reassembly buffer to avoid per-message allocations.
  // Grows as needed but never shrinks, reusing capacity across messages.
  private buffer = new Uint8Array(0);
  private bufferUsed = 0;

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
      this.bufferUsed = 0;

      let initResolved = false;

      ws.onopen = () => {
        this._connected = true;
      };

      ws.onmessage = (event: MessageEvent) => {
        const data = event.data as ArrayBuffer;
        if (data.byteLength < 1) return;

        const chunk = new Uint8Array(data);

        // FAST PATH: When the reassembly buffer is empty, the proxy has sent
        // a complete RFB message as a single WebSocket frame. Dispatch it
        // directly without buffering or scanning for message boundaries.
        // This eliminates expensive tryGetMessageLength() calls that scan
        // through all Tight tiles (~510 per full-screen FBU) just to find
        // the message length — work that's unnecessary when the proxy already
        // guarantees complete messages.
        if (this.bufferUsed === 0) {
          const peekView = new DataView(data);
          const msgType = chunk[0];

          // Extension messages
          if (msgType >= 128 && chunk.length >= 5) {
            const payloadLen = peekView.getUint32(1);
            if (chunk.length === 5 + payloadLen) {
              if (!initResolved && msgType === ExtSessionInit) {
                initResolved = true;
                resolve(parseSessionInit(peekView));
                return;
              }
              this.messageHandler?.(msgType, peekView);
              return;
            }
          } else if (msgType <= 3) {
            // Standard RFB message types (0=FBUpdate, 1=ColourMap, 2=Bell, 3=CutText).
            // The proxy sends each as a complete WebSocket frame, so we can
            // dispatch directly without scanning for message boundaries.
            if (msgType === 3) {
              const textLen = chunk.length >= 8 ? peekView.getUint32(4) : -1;
              console.debug('[VNC] ServerCutText received', { frameBytes: chunk.length, textLen });
            }
            this.messageHandler?.(msgType, peekView);
            return;
          }
        }

        // SLOW PATH: Buffer incomplete data for reassembly.
        // This handles edge cases where messages span WebSocket frames.
        const needed = this.bufferUsed + chunk.length;
        if (needed > this.buffer.length) {
          const newSize = Math.max(this.buffer.length * 2, needed);
          const newBuf = new Uint8Array(newSize);
          if (this.bufferUsed > 0) {
            newBuf.set(this.buffer.subarray(0, this.bufferUsed));
          }
          this.buffer = newBuf;
        }
        this.buffer.set(chunk, this.bufferUsed);
        this.bufferUsed += chunk.length;

        // Extract and dispatch complete messages
        let consumed = 0;
        while (consumed < this.bufferUsed) {
          const remaining = this.bufferUsed - consumed;
          const bufView = this.buffer.subarray(consumed, this.bufferUsed);

          if (bufView[0] >= 128 && remaining >= 5) {
            const extView = new DataView(this.buffer.buffer, this.buffer.byteOffset + consumed, remaining);
            const payloadLen = extView.getUint32(1);
            const extMsgLen = 5 + payloadLen;
            if (remaining < extMsgLen) break;

            const msgBytes = this.buffer.slice(consumed, consumed + extMsgLen);
            consumed += extMsgLen;
            const view = new DataView(msgBytes.buffer, msgBytes.byteOffset, msgBytes.byteLength);

            if (!initResolved && msgBytes[0] === ExtSessionInit) {
              initResolved = true;
              resolve(parseSessionInit(view));
              continue;
            }
            this.messageHandler?.(msgBytes[0], view);
            continue;
          }

          const msgLen = tryGetMessageLength(bufView);
          if (msgLen === -1 || msgLen > remaining) {
            break;
          }

          const msgBytes = this.buffer.slice(consumed, consumed + msgLen);
          consumed += msgLen;

          const view = new DataView(msgBytes.buffer, msgBytes.byteOffset, msgBytes.byteLength);
          const msgType = view.getUint8(0);

          this.messageHandler?.(msgType, view);
        }

        if (consumed > 0) {
          if (consumed < this.bufferUsed) {
            this.buffer.copyWithin(0, consumed, this.bufferUsed);
          }
          this.bufferUsed -= consumed;
        }
      };

      ws.onerror = () => {
        if (!initResolved) {
          reject(new Error('WebSocket connection failed'));
        }
      };

      ws.onclose = (event: CloseEvent) => {
        this._connected = false;
        this.bufferUsed = 0;
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
      this.bufferUsed = 0;
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
