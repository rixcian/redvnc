import { describe, it, expect } from 'vitest';
import { parseServerMessage, readCompactLength } from './rfb-parser';
import {
  MsgFramebufferUpdate,
  MsgBell,
  MsgServerCutText,
  ExtClipboardUpdate,
  ExtUploadStatus,
} from './types';

function makeDataView(bytes: number[]): DataView {
  const buf = new ArrayBuffer(bytes.length);
  const u8 = new Uint8Array(buf);
  u8.set(bytes);
  return new DataView(buf);
}

describe('parseServerMessage', () => {
  it('parses Bell message', () => {
    const view = makeDataView([MsgBell]);
    const msg = parseServerMessage(MsgBell, view);
    expect(msg).toEqual({ type: MsgBell });
  });

  it('returns null for unknown message type', () => {
    const view = makeDataView([99]);
    const msg = parseServerMessage(99, view);
    expect(msg).toBeNull();
  });

  it('parses disconnect sentinel', () => {
    const view = new DataView(new ArrayBuffer(0));
    const msg = parseServerMessage(-1, view);
    expect(msg).toEqual({ type: -1 });
  });

  it('parses ServerCutText message', () => {
    const text = 'hello';
    const textBytes = new TextEncoder().encode(text);
    const bytes = [
      MsgServerCutText, 0, 0, 0, // type + 3 padding
      0, 0, 0, textBytes.length,  // text length (uint32)
      ...textBytes,
    ];
    const view = makeDataView(bytes);
    const msg = parseServerMessage(MsgServerCutText, view);
    expect(msg).not.toBeNull();
    if (msg && 'text' in msg) {
      expect(msg.type).toBe(MsgServerCutText);
      expect(msg.text).toBe('hello');
    }
  });

  it('parses ClipboardUpdate extension message', () => {
    const text = 'world';
    const textBytes = new TextEncoder().encode(text);
    const payloadLen = 4 + textBytes.length;
    const bytes = [
      ExtClipboardUpdate,
      (payloadLen >> 24) & 0xff, (payloadLen >> 16) & 0xff,
      (payloadLen >> 8) & 0xff, payloadLen & 0xff,
      0, 0, 0, textBytes.length,
      ...textBytes,
    ];
    const view = makeDataView(bytes);
    const msg = parseServerMessage(ExtClipboardUpdate, view);
    expect(msg).not.toBeNull();
    if (msg && 'text' in msg) {
      expect(msg.text).toBe('world');
    }
  });

  it('parses UploadStatus extension message', () => {
    const msgText = 'ok';
    const msgBytes = new TextEncoder().encode(msgText);
    const payloadLen = 4 + 1 + 8 + 2 + msgBytes.length;
    const bytes = [
      ExtUploadStatus,
      (payloadLen >> 24) & 0xff, (payloadLen >> 16) & 0xff,
      (payloadLen >> 8) & 0xff, payloadLen & 0xff,
      0, 0, 0, 42, // uploadId = 42
      0,            // status = 0
      0, 0, 0, 0,   // bytesWritten hi
      0, 0, 4, 0,   // bytesWritten lo = 1024
      0, msgBytes.length, // msgLen
      ...msgBytes,
    ];
    const view = makeDataView(bytes);
    const msg = parseServerMessage(ExtUploadStatus, view);
    expect(msg).not.toBeNull();
    if (msg && 'uploadId' in msg) {
      expect(msg.uploadId).toBe(42);
      expect(msg.status).toBe(0);
      expect(msg.bytesWritten).toBe(1024);
      expect(msg.messageText).toBe('ok');
    }
  });

  it('parses FramebufferUpdate with Raw encoding', () => {
    // Build a 2x2 raw framebuffer update
    const w = 2, h = 2;
    const pixelData = new Array(w * h * 4).fill(0xff); // white pixels

    const bytes = [
      MsgFramebufferUpdate, 0, // type + padding
      0, 1, // numRects = 1
      // Rect header: x=0, y=0, width=2, height=2, encoding=0 (Raw)
      0, 0, 0, 0, 0, 2, 0, 2, 0, 0, 0, 0,
      ...pixelData,
    ];
    const view = makeDataView(bytes);
    const msg = parseServerMessage(MsgFramebufferUpdate, view);
    expect(msg).not.toBeNull();
    if (msg && 'rectangles' in msg) {
      expect(msg.rectangles.length).toBe(1);
      expect(msg.rectangles[0].header.width).toBe(2);
      expect(msg.rectangles[0].header.height).toBe(2);
      expect(msg.rectangles[0].header.encoding).toBe(0);
    }
  });
});

describe('readCompactLength', () => {
  it('reads 1-byte compact length', () => {
    const view = makeDataView([42]);
    const { length, bytesRead } = readCompactLength(view, 0);
    expect(length).toBe(42);
    expect(bytesRead).toBe(1);
  });

  it('reads 2-byte compact length', () => {
    // length = 0x80 | lower7, second byte has upper bits
    const view = makeDataView([0x80 | 10, 3]); // 10 + (3 << 7) = 10 + 384 = 394
    const { length, bytesRead } = readCompactLength(view, 0);
    expect(length).toBe(10 + (3 << 7));
    expect(bytesRead).toBe(2);
  });

  it('reads 3-byte compact length', () => {
    const view = makeDataView([0x80 | 10, 0x80 | 3, 1]); // 10 + (3 << 7) + (1 << 14)
    const { length, bytesRead } = readCompactLength(view, 0);
    expect(length).toBe(10 + (3 << 7) + (1 << 14));
    expect(bytesRead).toBe(3);
  });
});
