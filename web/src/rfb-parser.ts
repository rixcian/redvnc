import {
  MsgFramebufferUpdate,
  MsgSetColourMapEntry,
  MsgBell,
  MsgServerCutText,
  ExtClipboardUpdate,
  ExtUploadStatus,
  type RectHeader,
  type UploadProgress,
} from './types';

export interface FramebufferUpdateMessage {
  type: typeof MsgFramebufferUpdate;
  rectangles: RectangleData[];
}

export interface RectangleData {
  header: RectHeader;
  /** Raw bytes of the encoding data (after the rect header). */
  data: DataView;
}

export interface ServerCutTextMessage {
  type: typeof MsgServerCutText;
  text: string;
}

export interface ClipboardUpdateMessage {
  type: typeof ExtClipboardUpdate;
  text: string;
}

export interface UploadStatusMessage {
  type: typeof ExtUploadStatus;
  uploadId: number;
  status: number;
  bytesWritten: number;
  messageText: string;
}

export type ParsedMessage =
  | FramebufferUpdateMessage
  | { type: typeof MsgBell }
  | ServerCutTextMessage
  | ClipboardUpdateMessage
  | UploadStatusMessage
  | { type: -1 }; // disconnect sentinel

const decoder = new TextDecoder();

/**
 * Try to determine the total byte length of the RFB message starting at the
 * beginning of `buf`. Returns -1 if not enough data is available yet.
 * Used by the reassembly buffer to detect complete messages.
 */
export function tryGetMessageLength(buf: Uint8Array): number {
  if (buf.length < 1) return -1;
  const msgType = buf[0];

  // Extension messages: type(1) + length(4) + payload
  if (msgType >= 128) {
    if (buf.length < 5) return -1;
    const view = new DataView(buf.buffer, buf.byteOffset, buf.byteLength);
    const payloadLen = view.getUint32(1);
    return 5 + payloadLen;
  }

  switch (msgType) {
    case MsgFramebufferUpdate:
      return tryGetFBUpdateLength(buf);

    case MsgSetColourMapEntry: {
      // type(1) + padding(1) + firstColour(2) + numColours(2) + colours(numColours*6)
      if (buf.length < 6) return -1;
      const view = new DataView(buf.buffer, buf.byteOffset, buf.byteLength);
      const numColours = view.getUint16(4);
      return 6 + numColours * 6;
    }

    case MsgBell:
      return 1;

    case MsgServerCutText: {
      // type(1) + padding(3) + length(4) + text
      if (buf.length < 8) return -1;
      const view = new DataView(buf.buffer, buf.byteOffset, buf.byteLength);
      const textLen = view.getUint32(4);
      return 8 + textLen;
    }

    default:
      // Unknown message type - cannot determine length
      return -1;
  }
}

function tryGetFBUpdateLength(buf: Uint8Array): number {
  // type(1) + padding(1) + numRects(2) + rects...
  if (buf.length < 4) return -1;
  const view = new DataView(buf.buffer, buf.byteOffset, buf.byteLength);
  const numRects = view.getUint16(2);
  let offset = 4;

  for (let i = 0; i < numRects; i++) {
    if (buf.length < offset + 12) return -1;
    const width = view.getUint16(offset + 4);
    const height = view.getUint16(offset + 6);
    const encoding = view.getInt32(offset + 8);
    offset += 12;

    const dataLen = tryGetEncodingDataLength(width, height, encoding, view, offset);
    if (dataLen === -1) return -1;
    if (buf.length < offset + dataLen) return -1;
    offset += dataLen;
  }

  return offset;
}

function tryGetEncodingDataLength(
  width: number, height: number, encoding: number,
  view: DataView, offset: number,
): number {
  const remaining = view.byteLength - offset;

  switch (encoding) {
    case 0: // Raw
      return width * height * 4;
    case 1: // CopyRect
      return 4;
    case 6: // Zlib
      if (remaining < 4) return -1;
      return 4 + view.getUint32(offset);
    case 7: // Tight
      return tryGetTightDataLength(width, height, view, offset);
    case 16: // ZRLE
      if (remaining < 4) return -1;
      return 4 + view.getUint32(offset);
    case 50: // H.264
      // flags(4) + nalLen(4) + nalData
      if (remaining < 8) return -1;
      return 8 + view.getUint32(offset + 4);
    case -239: // Cursor
      return width * height * 4 + Math.ceil(width / 8) * height;
    case -223: // DesktopSize
      return 0;
    default:
      return 0;
  }
}

function tryGetTightDataLength(
  width: number, height: number, view: DataView, baseOffset: number,
): number {
  const bufLen = view.byteLength;
  let offset = baseOffset;

  for (let ty = 0; ty < height; ty += 64) {
    for (let tx = 0; tx < width; tx += 64) {
      if (offset >= bufLen) return -1;
      const control = view.getUint8(offset);
      offset += 1;

      if ((control & 0x0f) === 0x08) {
        // Solid fill: 3 bytes
        offset += 3;
        if (offset > bufLen) return -1;
      } else if ((control & 0x0f) === 0x09) {
        // JPEG: compact length + JPEG data
        const result = tryReadCompactLength(view, offset);
        if (result === null) return -1;
        offset += result.bytesRead + result.length;
        if (offset > bufLen) return -1;
      } else {
        // Basic (zlib): compact length + compressed data
        const result = tryReadCompactLength(view, offset);
        if (result === null) return -1;
        offset += result.bytesRead + result.length;
        if (offset > bufLen) return -1;
      }
    }
  }

  return offset - baseOffset;
}

function tryReadCompactLength(
  view: DataView, offset: number,
): { length: number; bytesRead: number } | null {
  if (offset >= view.byteLength) return null;
  let length = view.getUint8(offset) & 0x7f;
  let bytesRead = 1;

  if (view.getUint8(offset) & 0x80) {
    if (offset + 1 >= view.byteLength) return null;
    length |= (view.getUint8(offset + 1) & 0x7f) << 7;
    bytesRead = 2;
    if (view.getUint8(offset + 1) & 0x80) {
      if (offset + 2 >= view.byteLength) return null;
      length |= view.getUint8(offset + 2) << 14;
      bytesRead = 3;
    }
  }

  return { length, bytesRead };
}

/**
 * Parse a raw binary message received from the WebSocket into a structured message.
 * The DataView covers the entire WebSocket frame.
 */
export function parseServerMessage(
  msgType: number,
  view: DataView,
): ParsedMessage | null {
  switch (msgType) {
    case MsgFramebufferUpdate:
      return parseFramebufferUpdate(view);
    case MsgBell:
      return { type: MsgBell };
    case MsgServerCutText:
      return parseServerCutText(view);
    case ExtClipboardUpdate:
      return parseClipboardUpdate(view);
    case ExtUploadStatus:
      return parseUploadStatus(view);
    case -1:
      return { type: -1 };
    default:
      return null;
  }
}

function parseFramebufferUpdate(view: DataView): FramebufferUpdateMessage {
  // type(1) + padding(1) + numRects(2) + rects...
  const numRects = view.getUint16(2);
  const rectangles: RectangleData[] = [];
  let offset = 4;

  for (let i = 0; i < numRects; i++) {
    const header: RectHeader = {
      x: view.getUint16(offset),
      y: view.getUint16(offset + 2),
      width: view.getUint16(offset + 4),
      height: view.getUint16(offset + 6),
      encoding: view.getInt32(offset + 8),
    };
    offset += 12;

    const data = new DataView(view.buffer, view.byteOffset + offset);
    rectangles.push({ header, data });

    // Only compute encoding data length if there are more rects after this one.
    // For the last rect (and the common single-rect case), this skips the
    // expensive getTightDataLength() which scans all ~510 tiles per full-screen
    // Tight rectangle — work that's duplicated by the decoder anyway.
    if (i < numRects - 1) {
      offset += getEncodingDataLength(header, data);
    }
  }

  return { type: MsgFramebufferUpdate, rectangles };
}

/**
 * Calculate the byte length of the encoding data for a given rectangle.
 * This is needed to advance through multiple rectangles in a single FBUpdate.
 */
function getEncodingDataLength(header: RectHeader, data: DataView): number {
  const { width, height, encoding } = header;

  switch (encoding) {
    case 0: // Raw
      return width * height * 4; // assuming 32bpp after SetPixelFormat

    case 1: // CopyRect
      return 4; // srcX(2) + srcY(2)

    case 6: // Zlib
      // 4-byte length prefix + compressed data
      return 4 + data.getUint32(0);

    case 7: // Tight
      return getTightDataLength(width, height, data);

    case 16: // ZRLE
      // 4-byte length prefix + compressed data (same framing as Zlib)
      return 4 + data.getUint32(0);

    case 50: // H.264
      // flags(4) + nalLen(4) + nalData
      return 8 + data.getUint32(4);

    case -239: // Cursor
      return width * height * 4 + Math.ceil(width / 8) * height;

    case -223: // DesktopSize
      return 0;

    default:
      return 0;
  }
}

function getTightDataLength(width: number, height: number, data: DataView): number {
  let offset = 0;

  // Process tiles (Tight uses 64x64 tiles)
  for (let ty = 0; ty < height; ty += 64) {
    const tileH = Math.min(64, height - ty);
    for (let tx = 0; tx < width; tx += 64) {
      const tileW = Math.min(64, width - tx);
      const control = data.getUint8(offset);
      offset += 1;

      if ((control & 0x0f) === 0x08) {
        // Solid fill: 4 bytes (BGRA) or 3 bytes (RGB) depending on pixel format
        // After SetPixelFormat to RGBA 32bpp, the server uses the pixel format we set.
        // For tight, pixel data uses 3 bytes (RGB) when trueColour and bpp=32
        offset += 3;
      } else if ((control & 0x0f) === 0x09) {
        // JPEG: compact length + JPEG data
        const { length, bytesRead } = readCompactLength(data, offset);
        offset += bytesRead + length;
      } else {
        // Basic (zlib): compact length + zlib compressed data
        const { length, bytesRead } = readCompactLength(data, offset);
        offset += bytesRead + length;
      }
    }
  }

  return offset;
}

export function readCompactLength(data: DataView, offset: number): { length: number; bytesRead: number } {
  let length = data.getUint8(offset) & 0x7f;
  let bytesRead = 1;

  if (data.getUint8(offset) & 0x80) {
    length |= (data.getUint8(offset + 1) & 0x7f) << 7;
    bytesRead = 2;
    if (data.getUint8(offset + 1) & 0x80) {
      length |= data.getUint8(offset + 2) << 14;
      bytesRead = 3;
    }
  }

  return { length, bytesRead };
}

function parseServerCutText(view: DataView): ServerCutTextMessage {
  // type(1) + padding(3) + length(4) + text
  const textLen = view.getUint32(4);
  const textBytes = new Uint8Array(view.buffer, view.byteOffset + 8, textLen);
  return { type: MsgServerCutText, text: decoder.decode(textBytes) };
}

function parseClipboardUpdate(view: DataView): ClipboardUpdateMessage {
  // type(1) + length(4) + textLength(4) + text
  const textLen = view.getUint32(5);
  const textBytes = new Uint8Array(view.buffer, view.byteOffset + 9, textLen);
  return { type: ExtClipboardUpdate, text: decoder.decode(textBytes) };
}

function parseUploadStatus(view: DataView): UploadStatusMessage {
  // type(1) + length(4) + uploadId(4) + status(1) + bytesWritten(8) + msgLen(2) + msg
  const uploadId = view.getUint32(5);
  const status = view.getUint8(9);
  const bytesWrittenHi = view.getUint32(10);
  const bytesWrittenLo = view.getUint32(14);
  const bytesWritten = bytesWrittenHi * 0x100000000 + bytesWrittenLo;
  const msgLen = view.getUint16(18);
  const msgBytes = new Uint8Array(view.buffer, view.byteOffset + 20, msgLen);

  return {
    type: ExtUploadStatus,
    uploadId,
    status,
    bytesWritten,
    messageText: decoder.decode(msgBytes),
  };
}
