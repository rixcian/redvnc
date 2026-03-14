import {
  MsgSetPixelFormat,
  MsgSetEncodings,
  MsgFramebufferUpdateRequest,
  MsgKeyEvent,
  MsgPointerEvent,
  MsgClientCutText,
  ExtClipboardSet,
  ExtUploadBegin,
  ExtUploadChunk,
  ExtUploadEnd,
  ExtUploadCancel,
  type PixelFormat,
} from './types';

const encoder = new TextEncoder();

export function writeSetPixelFormat(pf: PixelFormat): ArrayBuffer {
  const buf = new ArrayBuffer(20);
  const view = new DataView(buf);
  view.setUint8(0, MsgSetPixelFormat);
  // 3 bytes padding (1-3)
  view.setUint8(4, pf.bitsPerPixel);
  view.setUint8(5, pf.depth);
  view.setUint8(6, pf.bigEndian ? 1 : 0);
  view.setUint8(7, pf.trueColour ? 1 : 0);
  view.setUint16(8, pf.redMax);
  view.setUint16(10, pf.greenMax);
  view.setUint16(12, pf.blueMax);
  view.setUint8(14, pf.redShift);
  view.setUint8(15, pf.greenShift);
  view.setUint8(16, pf.blueShift);
  // 3 bytes padding (17-19)
  return buf;
}

export function writeSetEncodings(encodings: number[]): ArrayBuffer {
  const buf = new ArrayBuffer(4 + encodings.length * 4);
  const view = new DataView(buf);
  view.setUint8(0, MsgSetEncodings);
  // 1 byte padding
  view.setUint16(2, encodings.length);
  for (let i = 0; i < encodings.length; i++) {
    view.setInt32(4 + i * 4, encodings[i]);
  }
  return buf;
}

export function writeFramebufferUpdateRequest(
  incremental: boolean,
  x: number,
  y: number,
  width: number,
  height: number,
): ArrayBuffer {
  const buf = new ArrayBuffer(10);
  const view = new DataView(buf);
  view.setUint8(0, MsgFramebufferUpdateRequest);
  view.setUint8(1, incremental ? 1 : 0);
  view.setUint16(2, x);
  view.setUint16(4, y);
  view.setUint16(6, width);
  view.setUint16(8, height);
  return buf;
}

export function writeKeyEvent(down: boolean, keysym: number): ArrayBuffer {
  const buf = new ArrayBuffer(8);
  const view = new DataView(buf);
  view.setUint8(0, MsgKeyEvent);
  view.setUint8(1, down ? 1 : 0);
  // 2 bytes padding
  view.setUint32(4, keysym);
  return buf;
}

export function writePointerEvent(buttonMask: number, x: number, y: number): ArrayBuffer {
  const buf = new ArrayBuffer(6);
  const view = new DataView(buf);
  view.setUint8(0, MsgPointerEvent);
  view.setUint8(1, buttonMask);
  view.setUint16(2, x);
  view.setUint16(4, y);
  return buf;
}

export function writeClientCutText(text: string): ArrayBuffer {
  const textBytes = encoder.encode(text);
  const buf = new ArrayBuffer(8 + textBytes.length);
  const view = new DataView(buf);
  view.setUint8(0, MsgClientCutText);
  // 3 bytes padding
  view.setUint32(4, textBytes.length);
  new Uint8Array(buf, 8).set(textBytes);
  return buf;
}

// ---- Extension message writers ----

export function writeClipboardSet(text: string): ArrayBuffer {
  const textBytes = encoder.encode(text);
  const payloadLen = 4 + textBytes.length;
  const buf = new ArrayBuffer(5 + payloadLen);
  const view = new DataView(buf);
  view.setUint8(0, ExtClipboardSet);
  view.setUint32(1, payloadLen);
  view.setUint32(5, textBytes.length);
  new Uint8Array(buf, 9).set(textBytes);
  return buf;
}

export function writeUploadBegin(
  uploadId: number,
  fileSize: number,
  dir: string,
  fileName: string,
): ArrayBuffer {
  const dirBytes = encoder.encode(dir);
  const nameBytes = encoder.encode(fileName);
  const payloadLen = 4 + 8 + 2 + dirBytes.length + 2 + nameBytes.length;
  const buf = new ArrayBuffer(5 + payloadLen);
  const view = new DataView(buf);

  view.setUint8(0, ExtUploadBegin);
  view.setUint32(1, payloadLen);
  view.setUint32(5, uploadId);
  // fileSize as uint64 (use two uint32s since JS doesn't have native u64)
  view.setUint32(9, Math.floor(fileSize / 0x100000000));
  view.setUint32(13, fileSize >>> 0);
  view.setUint16(17, dirBytes.length);
  new Uint8Array(buf, 19, dirBytes.length).set(dirBytes);
  const nameOffset = 19 + dirBytes.length;
  view.setUint16(nameOffset, nameBytes.length);
  new Uint8Array(buf, nameOffset + 2, nameBytes.length).set(nameBytes);
  return buf;
}

export function writeUploadChunk(
  uploadId: number,
  offset: number,
  chunk: Uint8Array,
): ArrayBuffer {
  const payloadLen = 4 + 8 + chunk.length;
  const buf = new ArrayBuffer(5 + payloadLen);
  const view = new DataView(buf);
  view.setUint8(0, ExtUploadChunk);
  view.setUint32(1, payloadLen);
  view.setUint32(5, uploadId);
  view.setUint32(9, Math.floor(offset / 0x100000000));
  view.setUint32(13, offset >>> 0);
  new Uint8Array(buf, 17).set(chunk);
  return buf;
}

export function writeUploadEnd(uploadId: number, crc32: number): ArrayBuffer {
  const buf = new ArrayBuffer(5 + 8);
  const view = new DataView(buf);
  view.setUint8(0, ExtUploadEnd);
  view.setUint32(1, 8);
  view.setUint32(5, uploadId);
  view.setUint32(9, crc32);
  return buf;
}

export function writeUploadCancel(uploadId: number): ArrayBuffer {
  const buf = new ArrayBuffer(5 + 4);
  const view = new DataView(buf);
  view.setUint8(0, ExtUploadCancel);
  view.setUint32(1, 4);
  view.setUint32(5, uploadId);
  return buf;
}
