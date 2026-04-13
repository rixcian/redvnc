// ---- RFB Client-to-Server message types ----
export const MsgSetPixelFormat = 0;
export const MsgSetEncodings = 2;
export const MsgFramebufferUpdateRequest = 3;
export const MsgKeyEvent = 4;
export const MsgPointerEvent = 5;
export const MsgClientCutText = 6;

// ---- RFB Server-to-Client message types ----
export const MsgFramebufferUpdate = 0;
export const MsgSetColourMapEntry = 1;
export const MsgBell = 2;
export const MsgServerCutText = 3;

// ---- Encoding types ----
export const EncodingRaw = 0;
export const EncodingCopyRect = 1;
export const EncodingRRE = 2;
export const EncodingZlib = 6;
export const EncodingTight = 7;
export const EncodingZRLE = 16;
export const EncodingCursor = -239;
export const EncodingDesktopSize = -223;

// ---- Extension message types ----
export const ExtSessionInit = 128;
export const ExtClipboardSet = 129;
export const ExtClipboardUpdate = 130;
export const ExtUploadBegin = 131;
export const ExtUploadChunk = 132;
export const ExtUploadEnd = 133;
export const ExtUploadStatus = 134;
export const ExtUploadCancel = 135;

// ---- Structures ----

export interface PixelFormat {
  bitsPerPixel: number;
  depth: number;
  bigEndian: boolean;
  trueColour: boolean;
  redMax: number;
  greenMax: number;
  blueMax: number;
  redShift: number;
  greenShift: number;
  blueShift: number;
}

export interface SessionInitData {
  width: number;
  height: number;
  pixelFormat: PixelFormat;
  name: string;
  authType: number;
}

export interface ConnectionStats {
  serverName: string;
  resolution: { width: number; height: number };
  authType: string;
  encodings: Record<string, number>;
  fps: number;
  totalRectangles: number;
  bytesReceived: number;
  /** Data rate in bytes per second (measured over the last 2 seconds). */
  dataRate: number;
  /** Round-trip latency in milliseconds (FBU request → response, averaged over recent samples). */
  latency: number;
}

export interface RectHeader {
  x: number;
  y: number;
  width: number;
  height: number;
  encoding: number;
}

export interface UploadProgress {
  uploadId: number;
  bytesWritten: number;
  totalBytes: number;
  percent: number;
}

export interface UploadResult {
  uploadId: number;
  success: boolean;
  message: string;
}

export interface UploadOptions {
  dir?: string;
}

export interface VncClientOptions {
  url: string;
  target: string;
  password?: string;
  viewOnly?: boolean;
  scaleToFit?: boolean;
  clipboardSync?: boolean;
  encodings?: number[];
  uploadDir?: string;
  reconnect?: boolean;              // default: true
  maxReconnectAttempts?: number;     // default: 10
  reconnectBaseDelay?: number;       // default: 1000 (ms)
  reconnectMaxDelay?: number;        // default: 30000 (ms)
}

/** Minimal surface for binding the viewer canvas (implemented by VncClient). */
export interface VncCanvasClient {
  attachCanvas(canvas: HTMLCanvasElement): void;
  detachCanvas(): void;
}

export interface VncViewerProps {
  client: VncCanvasClient | null;
  scaleToFit?: boolean;
  className?: string;
  style?: React.CSSProperties;
}

export type VncEventMap = {
  connect: () => void;
  disconnect: (reason: string) => void;
  reconnecting: (attempt: number) => void;
  reconnected: () => void;
  reconnect_failed: () => void;
  resize: (width: number, height: number) => void;
  bell: () => void;
  clipboard: (text: string) => void;
};

// The RGBA pixel format we request from the VNC server for direct ImageData compat
export const RGBA_PIXEL_FORMAT: PixelFormat = {
  bitsPerPixel: 32,
  depth: 24,
  bigEndian: false,
  trueColour: true,
  redMax: 255,
  greenMax: 255,
  blueMax: 255,
  redShift: 0,
  greenShift: 8,
  blueShift: 16,
};

// Clipboard history entry
export interface ClipboardEntry {
  id: number;
  text: string;
  source: 'local' | 'remote';
  timestamp: number; // Date.now()
}

// Default encoding preference order
export const DEFAULT_ENCODINGS = [
  EncodingTight,
  EncodingZRLE,
  EncodingZlib,
  EncodingCopyRect,
  EncodingRaw,
  EncodingCursor,
  EncodingDesktopSize,
];
