import { useEffect, useRef, useCallback, useState } from 'react';
import { VncClient } from '../index';
import type { VncViewerProps } from '../types';

export const VncViewer: React.FC<VncViewerProps> = ({
  url,
  target,
  password,
  viewOnly = false,
  scaleToFit = false,
  clipboardSync = true,
  uploadDir,
  onConnect,
  onDisconnect,
  onBell,
  className,
  style,
}) => {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const clientRef = useRef<VncClient | null>(null);
  const [status, setStatus] = useState<'disconnected' | 'connecting' | 'connected' | 'reconnecting'>('disconnected');
  const [error, setError] = useState<string | null>(null);
  const [reconnectAttempt, setReconnectAttempt] = useState(0);

  const connect = useCallback(async () => {
    // Clean up any existing client
    if (clientRef.current) {
      clientRef.current.disconnect();
      clientRef.current = null;
    }

    const client = new VncClient({
      url,
      target,
      password,
      viewOnly,
      scaleToFit,
      clipboardSync,
      uploadDir,
    });

    clientRef.current = client;

    client.on('connect', () => {
      setStatus('connected');
      setError(null);
      onConnect?.();
    });

    client.on('disconnect', (reason) => {
      setStatus('disconnected');
      onDisconnect?.(reason);
    });

    client.on('reconnecting', (attempt) => {
      setStatus('reconnecting');
      setReconnectAttempt(attempt);
    });

    client.on('reconnected', () => {
      setStatus('connected');
      setReconnectAttempt(0);
      setError(null);
    });

    client.on('reconnect_failed', () => {
      setStatus('disconnected');
      setError('Failed to reconnect after multiple attempts');
    });

    if (onBell) {
      client.on('bell', onBell);
    }

    setStatus('connecting');
    setError(null);

    try {
      await client.connect();

      if (canvasRef.current) {
        client.attachCanvas(canvasRef.current);
      }
    } catch (err) {
      setStatus('disconnected');
      setError(err instanceof Error ? err.message : 'Connection failed');
    }
  }, [url, target, password, viewOnly, scaleToFit, clipboardSync, uploadDir, onConnect, onDisconnect, onBell]);

  useEffect(() => {
    connect();

    return () => {
      if (clientRef.current) {
        clientRef.current.disconnect();
        clientRef.current = null;
      }
    };
  }, [connect]);

  return (
    <div className={className} style={{ position: 'relative', ...style }}>
      {error && (
        <div style={{
          position: 'absolute',
          top: '50%',
          left: '50%',
          transform: 'translate(-50%, -50%)',
          color: '#ff4444',
          background: 'rgba(0,0,0,0.8)',
          padding: '16px 24px',
          borderRadius: '8px',
          zIndex: 10,
        }}>
          {error}
        </div>
      )}
      {status === 'reconnecting' && (
        <div style={{
          position: 'absolute',
          top: '50%',
          left: '50%',
          transform: 'translate(-50%, -50%)',
          color: '#ffaa00',
          background: 'rgba(0,0,0,0.8)',
          padding: '16px 24px',
          borderRadius: '8px',
          zIndex: 10,
          textAlign: 'center',
        }}>
          Reconnecting... (attempt {reconnectAttempt})
        </div>
      )}
      {status === 'connecting' && (
        <div style={{
          position: 'absolute',
          top: '50%',
          left: '50%',
          transform: 'translate(-50%, -50%)',
          color: '#ffffff',
          background: 'rgba(0,0,0,0.8)',
          padding: '16px 24px',
          borderRadius: '8px',
          zIndex: 10,
        }}>
          Connecting...
        </div>
      )}
      <canvas
        ref={canvasRef}
        style={{
          display: 'block',
          width: scaleToFit ? '100%' : undefined,
          height: scaleToFit ? '100%' : undefined,
          outline: 'none',
        }}
      />
    </div>
  );
};
