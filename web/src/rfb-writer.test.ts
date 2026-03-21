import { describe, it, expect } from 'vitest';
import {
  writeSetPixelFormat,
  writeSetEncodings,
  writeFramebufferUpdateRequest,
  writeKeyEvent,
  writePointerEvent,
  writeClientCutText,
  writeClipboardSet,
  writeUploadEnd,
  writeUploadCancel,
} from './rfb-writer';
import {
  MsgSetPixelFormat,
  MsgSetEncodings,
  MsgFramebufferUpdateRequest,
  MsgKeyEvent,
  MsgPointerEvent,
  MsgClientCutText,
  ExtClipboardSet,
  ExtUploadEnd,
  ExtUploadCancel,
  RGBA_PIXEL_FORMAT,
} from './types';

describe('writeSetPixelFormat', () => {
  it('serializes RGBA pixel format correctly', () => {
    const buf = writeSetPixelFormat(RGBA_PIXEL_FORMAT);
    const view = new DataView(buf);
    expect(buf.byteLength).toBe(20);
    expect(view.getUint8(0)).toBe(MsgSetPixelFormat);
    expect(view.getUint8(4)).toBe(32); // bitsPerPixel
    expect(view.getUint8(5)).toBe(24); // depth
    expect(view.getUint8(6)).toBe(0);  // bigEndian = false
    expect(view.getUint8(7)).toBe(1);  // trueColour = true
    expect(view.getUint16(8)).toBe(255); // redMax
    expect(view.getUint16(10)).toBe(255); // greenMax
    expect(view.getUint16(12)).toBe(255); // blueMax
    expect(view.getUint8(14)).toBe(0);  // redShift
    expect(view.getUint8(15)).toBe(8);  // greenShift
    expect(view.getUint8(16)).toBe(16); // blueShift
  });
});

describe('writeSetEncodings', () => {
  it('serializes encoding list', () => {
    const encodings = [7, 6, 0]; // Tight, Zlib, Raw
    const buf = writeSetEncodings(encodings);
    const view = new DataView(buf);
    expect(buf.byteLength).toBe(4 + 3 * 4);
    expect(view.getUint8(0)).toBe(MsgSetEncodings);
    expect(view.getUint16(2)).toBe(3);
    expect(view.getInt32(4)).toBe(7);
    expect(view.getInt32(8)).toBe(6);
    expect(view.getInt32(12)).toBe(0);
  });

  it('handles negative encoding values (pseudo-encodings)', () => {
    const encodings = [-239, -223]; // Cursor, DesktopSize
    const buf = writeSetEncodings(encodings);
    const view = new DataView(buf);
    expect(view.getInt32(4)).toBe(-239);
    expect(view.getInt32(8)).toBe(-223);
  });
});

describe('writeFramebufferUpdateRequest', () => {
  it('serializes incremental request', () => {
    const buf = writeFramebufferUpdateRequest(true, 10, 20, 800, 600);
    const view = new DataView(buf);
    expect(buf.byteLength).toBe(10);
    expect(view.getUint8(0)).toBe(MsgFramebufferUpdateRequest);
    expect(view.getUint8(1)).toBe(1); // incremental
    expect(view.getUint16(2)).toBe(10);
    expect(view.getUint16(4)).toBe(20);
    expect(view.getUint16(6)).toBe(800);
    expect(view.getUint16(8)).toBe(600);
  });

  it('serializes non-incremental request', () => {
    const buf = writeFramebufferUpdateRequest(false, 0, 0, 1024, 768);
    const view = new DataView(buf);
    expect(view.getUint8(1)).toBe(0); // not incremental
  });
});

describe('writeKeyEvent', () => {
  it('serializes key down event', () => {
    const buf = writeKeyEvent(true, 0xff0d); // Return key
    const view = new DataView(buf);
    expect(buf.byteLength).toBe(8);
    expect(view.getUint8(0)).toBe(MsgKeyEvent);
    expect(view.getUint8(1)).toBe(1); // down
    expect(view.getUint32(4)).toBe(0xff0d);
  });

  it('serializes key up event', () => {
    const buf = writeKeyEvent(false, 0x61); // 'a'
    const view = new DataView(buf);
    expect(view.getUint8(1)).toBe(0); // up
    expect(view.getUint32(4)).toBe(0x61);
  });
});

describe('writePointerEvent', () => {
  it('serializes pointer with button mask', () => {
    const buf = writePointerEvent(0x01, 400, 300); // left button
    const view = new DataView(buf);
    expect(buf.byteLength).toBe(6);
    expect(view.getUint8(0)).toBe(MsgPointerEvent);
    expect(view.getUint8(1)).toBe(1);
    expect(view.getUint16(2)).toBe(400);
    expect(view.getUint16(4)).toBe(300);
  });
});

describe('writeClientCutText', () => {
  it('serializes clipboard text', () => {
    const buf = writeClientCutText('hello');
    const view = new DataView(buf);
    expect(view.getUint8(0)).toBe(MsgClientCutText);
    expect(view.getUint32(4)).toBe(5);
    const textBytes = new Uint8Array(buf, 8);
    expect(new TextDecoder().decode(textBytes)).toBe('hello');
  });
});

describe('writeClipboardSet', () => {
  it('serializes extension clipboard message', () => {
    const buf = writeClipboardSet('test');
    const view = new DataView(buf);
    expect(view.getUint8(0)).toBe(ExtClipboardSet);
    const payloadLen = view.getUint32(1);
    expect(payloadLen).toBe(4 + 4); // textLen(4) + text(4)
    expect(view.getUint32(5)).toBe(4); // textLength
  });
});

describe('writeUploadEnd', () => {
  it('serializes upload end with CRC', () => {
    const buf = writeUploadEnd(42, 0xDEADBEEF);
    const view = new DataView(buf);
    expect(view.getUint8(0)).toBe(ExtUploadEnd);
    expect(view.getUint32(5)).toBe(42);
    expect(view.getUint32(9)).toBe(0xDEADBEEF);
  });
});

describe('writeUploadCancel', () => {
  it('serializes upload cancel', () => {
    const buf = writeUploadCancel(99);
    const view = new DataView(buf);
    expect(view.getUint8(0)).toBe(ExtUploadCancel);
    expect(view.getUint32(5)).toBe(99);
  });
});
