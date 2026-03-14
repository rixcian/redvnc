import {
  writeUploadBegin,
  writeUploadChunk,
  writeUploadEnd,
  writeUploadCancel,
} from './rfb-writer';
import type { UploadProgress, UploadResult, UploadOptions } from './types';
import type { SendFn } from './input';

const CHUNK_SIZE = 64 * 1024; // 64KB per chunk

/**
 * Compute CRC-32 (IEEE) of a Uint8Array.
 */
function crc32(data: Uint8Array): number {
  let crc = 0xffffffff;
  for (let i = 0; i < data.length; i++) {
    crc ^= data[i];
    for (let j = 0; j < 8; j++) {
      crc = (crc >>> 1) ^ (crc & 1 ? 0xedb88320 : 0);
    }
  }
  return (crc ^ 0xffffffff) >>> 0;
}

export class FileUploadHandler {
  private sendFn: SendFn;
  private nextUploadId = 1;
  private defaultDir: string;
  private progressCallback: ((progress: UploadProgress) => void) | null = null;
  private pendingUploads = new Map<number, {
    resolve: (result: UploadResult) => void;
    reject: (err: Error) => void;
    totalBytes: number;
  }>();

  constructor(sendFn: SendFn, defaultDir: string = '') {
    this.sendFn = sendFn;
    this.defaultDir = defaultDir;
  }

  setUploadDir(path: string): void {
    this.defaultDir = path;
  }

  onUploadProgress(callback: (progress: UploadProgress) => void): void {
    this.progressCallback = callback;
  }

  /**
   * Handle an UploadStatus message from the proxy.
   */
  handleUploadStatus(uploadId: number, status: number, bytesWritten: number, message: string): void {
    const pending = this.pendingUploads.get(uploadId);
    if (!pending) return;

    if (this.progressCallback) {
      this.progressCallback({
        uploadId,
        bytesWritten,
        totalBytes: pending.totalBytes,
        percent: pending.totalBytes > 0 ? (bytesWritten / pending.totalBytes) * 100 : 0,
      });
    }

    // Final status (after UploadEnd)
    if (bytesWritten >= pending.totalBytes || status !== 0) {
      this.pendingUploads.delete(uploadId);
      pending.resolve({
        uploadId,
        success: status === 0,
        message,
      });
    }
  }

  /**
   * Upload a file to the remote machine via the proxy.
   */
  async uploadFile(file: File, options?: UploadOptions): Promise<UploadResult> {
    const uploadId = this.nextUploadId++;
    const dir = options?.dir ?? this.defaultDir;
    const totalBytes = file.size;

    return new Promise<UploadResult>(async (resolve, reject) => {
      this.pendingUploads.set(uploadId, { resolve, reject, totalBytes });

      // Send UploadBegin
      this.sendFn(writeUploadBegin(uploadId, file.size, dir, file.name));

      // Read and send chunks
      const arrayBuffer = await file.arrayBuffer();
      const fileData = new Uint8Array(arrayBuffer);

      let offset = 0;
      while (offset < fileData.length) {
        const end = Math.min(offset + CHUNK_SIZE, fileData.length);
        const chunk = fileData.subarray(offset, end);
        this.sendFn(writeUploadChunk(uploadId, offset, chunk));
        offset = end;
      }

      // Compute CRC-32 and send UploadEnd
      const checksum = crc32(fileData);
      this.sendFn(writeUploadEnd(uploadId, checksum));
    });
  }

  /**
   * Cancel an in-progress upload.
   */
  cancelUpload(uploadId: number): void {
    this.sendFn(writeUploadCancel(uploadId));
    const pending = this.pendingUploads.get(uploadId);
    if (pending) {
      this.pendingUploads.delete(uploadId);
      pending.resolve({
        uploadId,
        success: false,
        message: 'cancelled',
      });
    }
  }

  /**
   * Clean up all pending uploads on disconnect.
   */
  cleanup(): void {
    for (const [id, pending] of this.pendingUploads) {
      pending.resolve({
        uploadId: id,
        success: false,
        message: 'disconnected',
      });
    }
    this.pendingUploads.clear();
  }
}
