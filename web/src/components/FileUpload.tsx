import { useCallback, useState, useRef } from 'react';
import type { VncClient } from '../index';
import type { UploadProgress } from '../types';

interface FileUploadDropZoneProps {
  client: VncClient | null;
  children?: React.ReactNode;
  className?: string;
  style?: React.CSSProperties;
}

export const FileUploadDropZone: React.FC<FileUploadDropZoneProps> = ({
  client,
  children,
  className,
  style,
}) => {
  const [isDragging, setIsDragging] = useState(false);
  const [progress, setProgress] = useState<UploadProgress | null>(null);
  const dragCounter = useRef(0);

  const handleDragEnter = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    dragCounter.current++;
    setIsDragging(true);
  }, []);

  const handleDragLeave = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    dragCounter.current--;
    if (dragCounter.current === 0) {
      setIsDragging(false);
    }
  }, []);

  const handleDragOver = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
  }, []);

  const handleDrop = useCallback(async (e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setIsDragging(false);
    dragCounter.current = 0;

    if (!client?.connected) return;

    const files = Array.from(e.dataTransfer.files);
    if (files.length === 0) return;

    client.onUploadProgress((p) => setProgress(p));

    for (const file of files) {
      await client.uploadFile(file);
    }

    setProgress(null);
  }, [client]);

  return (
    <div
      className={className}
      style={{
        position: 'relative',
        ...style,
      }}
      onDragEnter={handleDragEnter}
      onDragLeave={handleDragLeave}
      onDragOver={handleDragOver}
      onDrop={handleDrop}
    >
      {children}
      {isDragging && (
        <div style={{
          position: 'absolute',
          inset: 0,
          background: 'rgba(0, 120, 255, 0.2)',
          border: '2px dashed #0078ff',
          borderRadius: '8px',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          color: '#ffffff',
          fontSize: '18px',
          fontWeight: 'bold',
          zIndex: 100,
          pointerEvents: 'none',
        }}>
          Drop files to upload
        </div>
      )}
      {progress && (
        <div style={{
          position: 'absolute',
          bottom: '16px',
          left: '50%',
          transform: 'translateX(-50%)',
          background: 'rgba(0,0,0,0.8)',
          color: '#ffffff',
          padding: '8px 16px',
          borderRadius: '4px',
          zIndex: 100,
          fontSize: '14px',
        }}>
          Uploading: {progress.percent.toFixed(1)}%
        </div>
      )}
    </div>
  );
};
