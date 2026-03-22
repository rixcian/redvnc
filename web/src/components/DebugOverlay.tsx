import { useEffect, useState } from 'react';
import type { VncClient } from '../index';
import type { ConnectionStats } from '../types';

interface DebugOverlayProps {
  client: VncClient | null;
  visible: boolean;
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function formatRate(bytesPerSec: number): string {
  if (bytesPerSec < 1024) return `${Math.round(bytesPerSec)} B/s`;
  if (bytesPerSec < 1024 * 1024) return `${(bytesPerSec / 1024).toFixed(1)} KB/s`;
  return `${(bytesPerSec / (1024 * 1024)).toFixed(1)} MB/s`;
}

export const DebugOverlay: React.FC<DebugOverlayProps> = ({ client, visible }) => {
  const [stats, setStats] = useState<ConnectionStats | null>(null);

  useEffect(() => {
    if (!visible || !client?.connected) {
      setStats(null);
      return;
    }

    const interval = setInterval(() => {
      if (client.connected) {
        setStats(client.getStats());
      }
    }, 500);

    // Fetch immediately
    setStats(client.getStats());

    return () => clearInterval(interval);
  }, [client, visible]);

  if (!visible || !stats) return null;

  const encodingEntries = Object.entries(stats.encodings)
    .sort(([, a], [, b]) => (b as number) - (a as number));

  return (
    <div style={{
      position: 'absolute',
      top: 8,
      left: 8,
      background: 'rgba(0, 0, 0, 0.8)',
      color: '#e0e0e0',
      padding: '10px 14px',
      borderRadius: 6,
      fontSize: 12,
      fontFamily: 'monospace',
      lineHeight: 1.6,
      zIndex: 20,
      pointerEvents: 'none',
      minWidth: 200,
      backdropFilter: 'blur(4px)',
      border: '1px solid rgba(255,255,255,0.1)',
    }}>
      <div style={{ fontWeight: 'bold', marginBottom: 6, color: '#fff', fontSize: 13 }}>
        Connection Info
      </div>

      <Row label="Server" value={stats.serverName} />
      <Row label="Resolution" value={`${stats.resolution.width}x${stats.resolution.height}`} />
      <Row label="Auth" value={stats.authType} />
      <Row label="Renderer" value={client?.rendererType ?? 'unknown'} />
      <Row label="FPS" value={String(stats.fps)} />
      <Row label="Data rate" value={formatRate(stats.dataRate)} />
      <Row label="Data received" value={formatBytes(stats.bytesReceived)} />
      <Row label="Total rects" value={stats.totalRectangles.toLocaleString()} />

      {encodingEntries.length > 0 && (
        <>
          <div style={{ borderTop: '1px solid rgba(255,255,255,0.15)', margin: '6px 0' }} />
          <div style={{ fontWeight: 'bold', marginBottom: 4, color: '#fff' }}>Encodings</div>
          {encodingEntries.map(([name, count]) => (
            <Row key={name} label={name} value={count.toLocaleString()} />
          ))}
        </>
      )}
    </div>
  );
};

const Row: React.FC<{ label: string; value: string }> = ({ label, value }) => (
  <div style={{ display: 'flex', justifyContent: 'space-between', gap: 16 }}>
    <span style={{ color: '#999' }}>{label}</span>
    <span>{value}</span>
  </div>
);
