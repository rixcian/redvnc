import { useCallback, useRef, useState } from 'react';
import type { VncClient } from '../index';
import { ClipboardPanel } from './ClipboardPanel';

interface ToolbarProps {
  client: VncClient | null;
  className?: string;
  style?: React.CSSProperties;
}

export const Toolbar: React.FC<ToolbarProps> = ({ client, className, style }) => {
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [showClipboard, setShowClipboard] = useState(false);

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

  const activeButtonStyle: React.CSSProperties = {
    ...buttonStyle,
    background: '#4a6cf7',
    borderColor: '#4a6cf7',
  };

  return (
    <div
      className={className}
      style={{
        position: 'relative',
        display: 'flex',
        gap: '8px',
        padding: '8px',
        background: '#1a1a1a',
        borderBottom: '1px solid #333',
        ...style,
      }}
    >
      <button
        style={showClipboard ? activeButtonStyle : buttonStyle}
        onClick={() => setShowClipboard(v => !v)}
      >
        Clipboard
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
      <ClipboardPanel
        client={client}
        visible={showClipboard}
        onClose={() => setShowClipboard(false)}
      />
    </div>
  );
};
