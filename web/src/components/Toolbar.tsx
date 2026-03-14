import { useCallback, useRef } from 'react';
import type { VncClient } from '../index';

interface ToolbarProps {
  client: VncClient | null;
  className?: string;
  style?: React.CSSProperties;
}

export const Toolbar: React.FC<ToolbarProps> = ({ client, className, style }) => {
  const fileInputRef = useRef<HTMLInputElement>(null);

  const handleClipboardPaste = useCallback(async () => {
    if (!client?.connected) return;
    try {
      const text = await navigator.clipboard.readText();
      client.sendClipboard(text);
    } catch {
      // Clipboard API requires user permission
    }
  }, [client]);

  const handleFileUpload = useCallback(() => {
    fileInputRef.current?.click();
  }, []);

  const handleFileChange = useCallback(async (e: React.ChangeEvent<HTMLInputElement>) => {
    if (!client?.connected || !e.target.files) return;
    for (const file of Array.from(e.target.files)) {
      await client.uploadFile(file);
    }
    e.target.value = '';
  }, [client]);

  const handleFullscreen = useCallback(() => {
    const el = document.querySelector('canvas');
    if (el) {
      el.requestFullscreen?.();
    }
  }, []);

  const buttonStyle: React.CSSProperties = {
    padding: '6px 12px',
    background: '#333',
    color: '#fff',
    border: '1px solid #555',
    borderRadius: '4px',
    cursor: 'pointer',
    fontSize: '13px',
  };

  return (
    <div
      className={className}
      style={{
        display: 'flex',
        gap: '8px',
        padding: '8px',
        background: '#1a1a1a',
        borderBottom: '1px solid #333',
        ...style,
      }}
    >
      <button style={buttonStyle} onClick={handleClipboardPaste}>
        Paste Clipboard
      </button>
      <button style={buttonStyle} onClick={handleFileUpload}>
        Upload File
      </button>
      <button style={buttonStyle} onClick={handleFullscreen}>
        Fullscreen
      </button>
      <input
        ref={fileInputRef}
        type="file"
        multiple
        style={{ display: 'none' }}
        onChange={handleFileChange}
      />
    </div>
  );
};
