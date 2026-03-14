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

    // The remaining bytes after the header for this rect are the encoding data.
    // We pass a DataView starting at the encoding data offset.
    // The decoder will need to figure out the length based on encoding type.
    const data = new DataView(view.buffer, view.byteOffset + offset);
    rectangles.push({ header, data });

    // Advance offset based on encoding
    offset += getEncodingDataLength(header, data);
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
