import { useState, useEffect, useCallback, useRef } from 'react';
import type { VncClient } from '../index';
import type { ClipboardEntry } from '../types';

interface ClipboardPanelProps {
  client: VncClient | null;
  visible: boolean;
  onClose: () => void;
}

function timeAgo(timestamp: number): string {
  const seconds = Math.floor((Date.now() - timestamp) / 1000);
  if (seconds < 60) return 'just now';
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.floor(minutes / 60);
  return `${hours}h ago`;
}

export const ClipboardPanel: React.FC<ClipboardPanelProps> = ({ client, visible, onClose }) => {
  const [history, setHistory] = useState<ClipboardEntry[]>([]);
  const [autoSync, setAutoSync] = useState(true);
  const [manualText, setManualText] = useState('');
  const [, setTick] = useState(0);
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  // Subscribe to history changes
  useEffect(() => {
    if (!client) return;
    setHistory(client.getClipboardHistory());
    setAutoSync(client.clipboardAutoSync);
    client.onClipboardHistory((entries) => setHistory(entries));
  }, [client]);

  // Refresh relative timestamps every 30s
  useEffect(() => {
    if (!visible) return;
    const id = setInterval(() => setTick(t => t + 1), 30_000);
    return () => clearInterval(id);
  }, [visible]);

  const handleAutoSyncToggle = useCallback(() => {
    if (!client) return;
    const next = !autoSync;
    setAutoSync(next);
    client.clipboardAutoSync = next;
  }, [client, autoSync]);

  const handlePaste = useCallback((text: string) => {
    client?.sendClipboard(text);
  }, [client]);

  const handleManualSend = useCallback(() => {
    if (!client || !manualText.trim()) return;
    client.sendClipboard(manualText);
    setManualText('');
  }, [client, manualText]);

  const handleTextareaKeyDown = useCallback((e: React.KeyboardEvent) => {
    // Stop propagation so VNC input handler doesn't capture keys typed here
    e.stopPropagation();
  }, []);

  if (!visible) return null;

  return (
    <div style={{
      position: 'absolute',
      top: 0,
      right: 0,
      width: 380,
      maxHeight: '100%',
      background: '#1a1a2e',
      borderLeft: '1px solid #2a2a4a',
      display: 'flex',
      flexDirection: 'column',
      zIndex: 30,
      fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif',
      fontSize: 14,
      color: '#c8c8e0',
      overflow: 'hidden',
    }}>
      {/* Header */}
      <div style={{
        display: 'flex',
        justifyContent: 'space-between',
        alignItems: 'center',
        padding: '16px 20px',
        borderBottom: '1px solid #2a2a4a',
      }}>
        <span style={{ fontSize: 18, fontWeight: 600, color: '#fff' }}>Clipboard Sync</span>
        <button
          onClick={onClose}
          style={{
            background: 'none',
            border: 'none',
            color: '#888',
            fontSize: 20,
            cursor: 'pointer',
            padding: '0 4px',
            lineHeight: 1,
          }}
        >
          &times;
        </button>
      </div>

      {/* Auto-sync toggle */}
      <div style={{
        display: 'flex',
        justifyContent: 'space-between',
        alignItems: 'center',
        padding: '12px 20px',
        borderBottom: '1px solid #2a2a4a',
      }}>
        <span style={{ color: '#999' }}>Auto-sync enabled</span>
        <button
          onClick={handleAutoSyncToggle}
          style={{
            width: 44,
            height: 24,
            borderRadius: 12,
            border: 'none',
            cursor: 'pointer',
            position: 'relative',
            background: autoSync ? '#4a6cf7' : '#444',
            transition: 'background 0.2s',
          }}
        >
          <span style={{
            display: 'block',
            width: 18,
            height: 18,
            borderRadius: 9,
            background: '#fff',
            position: 'absolute',
            top: 3,
            left: autoSync ? 23 : 3,
            transition: 'left 0.2s',
          }} />
        </button>
      </div>

      {/* History */}
      <div style={{ flex: 1, overflowY: 'auto', padding: '12px 20px' }}>
        <div style={{
          fontSize: 11,
          fontWeight: 600,
          color: '#666',
          letterSpacing: '0.08em',
          textTransform: 'uppercase',
          marginBottom: 10,
        }}>
          History
        </div>

        {history.length === 0 && (
          <div style={{ color: '#555', fontStyle: 'italic', padding: '12px 0' }}>
            No clipboard activity yet.
          </div>
        )}

        {history.map((entry, index) => (
          <div
            key={entry.id}
            style={{
              background: index === 0 ? 'rgba(74, 108, 247, 0.1)' : '#16162a',
              border: index === 0 ? '1px solid rgba(74, 108, 247, 0.4)' : '1px solid #2a2a4a',
              borderRadius: 8,
              padding: '12px 14px',
              marginBottom: 8,
              position: 'relative',
            }}
          >
            <div style={{
              color: index === 0 ? '#a8b8ff' : '#888',
              fontSize: 13,
              lineHeight: 1.4,
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-all',
              maxHeight: 60,
              overflow: 'hidden',
            }}>
              {entry.text.length > 120 ? entry.text.slice(0, 120) + '...' : entry.text}
            </div>
            <div style={{
              display: 'flex',
              justifyContent: 'space-between',
              alignItems: 'center',
              marginTop: 6,
            }}>
              <span style={{ fontSize: 11, color: '#555' }}>
                {timeAgo(entry.timestamp)} &middot; from {entry.source === 'local' ? 'Local' : 'Remote'}
              </span>
              <button
                onClick={() => handlePaste(entry.text)}
                style={{
                  background: 'none',
                  border: 'none',
                  color: '#4a6cf7',
                  fontSize: 12,
                  fontWeight: 600,
                  cursor: 'pointer',
                  padding: '2px 0',
                  letterSpacing: '0.03em',
                }}
              >
                PASTE &rarr;
              </button>
            </div>
          </div>
        ))}
      </div>

      {/* Manual paste */}
      <div style={{
        borderTop: '1px solid #2a2a4a',
        padding: '16px 20px',
      }}>
        <div style={{ fontWeight: 600, color: '#fff', marginBottom: 4 }}>Manual paste</div>
        <div style={{ fontSize: 12, color: '#666', marginBottom: 10 }}>
          Type or paste text here and click &quot;Send&quot; to push directly to the remote clipboard.
        </div>
        <textarea
          ref={textareaRef}
          value={manualText}
          onChange={e => setManualText(e.target.value)}
          onKeyDown={handleTextareaKeyDown}
          onKeyUp={e => e.stopPropagation()}
          placeholder="Paste content here..."
          style={{
            width: '100%',
            height: 64,
            background: '#0e0e1c',
            border: '1px solid #2a2a4a',
            borderRadius: 6,
            color: '#c8c8e0',
            padding: '8px 10px',
            fontSize: 13,
            fontFamily: 'monospace',
            resize: 'none',
            outline: 'none',
            boxSizing: 'border-box',
          }}
        />
        <button
          onClick={handleManualSend}
          disabled={!manualText.trim()}
          style={{
            marginTop: 8,
            padding: '8px 20px',
            background: manualText.trim() ? '#4a6cf7' : '#333',
            color: '#fff',
            border: 'none',
            borderRadius: 6,
            cursor: manualText.trim() ? 'pointer' : 'default',
            fontSize: 13,
            fontWeight: 600,
          }}
        >
          Send to Remote
        </button>
      </div>
    </div>
  );
};
