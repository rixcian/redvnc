import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { createRoot } from 'react-dom/client';
import {
  VncClient,
  VncViewer,
  DebugOverlay,
  Toolbar,
  ConnectionForm,
} from '../src/index';

function App() {
  const [wsUrl, setWsUrl] = useState('ws://localhost:8080/ws');
  const [target, setTarget] = useState('');
  const [password, setPassword] = useState('');
  const [sessionOpen, setSessionOpen] = useState(false);

  const handleConnect = (e: React.FormEvent) => {
    e.preventDefault();
    if (!target) return;
    setSessionOpen(true);
  };

  const handleLeaveSession = useCallback(() => {
    setSessionOpen(false);
  }, []);

  if (!sessionOpen) {
    return (
      <ConnectionForm
        wsUrl={wsUrl}
        target={target}
        password={password}
        onWsUrlChange={setWsUrl}
        onTargetChange={setTarget}
        onPasswordChange={setPassword}
        onSubmit={handleConnect}
      />
    );
  }

  return (
    <div className="flex h-screen w-screen flex-col bg-zinc-950">
      <VncSession
        wsUrl={wsUrl}
        target={target}
        password={password || undefined}
        onLeave={handleLeaveSession}
      />
    </div>
  );
}

type SessionStatus = 'disconnected' | 'connecting' | 'connected' | 'reconnecting';

function VncSession({
  wsUrl,
  target,
  password,
  onLeave,
}: {
  wsUrl: string;
  target: string;
  password?: string;
  onLeave: () => void;
}) {
  const [status, setStatus] = useState<SessionStatus>('disconnected');
  const [error, setError] = useState<string | null>(null);
  const [reconnectAttempt, setReconnectAttempt] = useState(0);
  const [showDebug, setShowDebug] = useState(false);
  const [client, setClient] = useState<VncClient | null>(null);

  const toolbarStatus = useMemo(() => {
    if (error) return 'error' as const;
    if (status === 'connecting') return 'connecting' as const;
    if (status === 'reconnecting') return 'reconnecting' as const;
    if (status === 'connected') return 'connected' as const;
    return 'idle' as const;
  }, [error, status]);

  useEffect(() => {
    let cancelled = false;
    const vnc = new VncClient({
      url: wsUrl,
      target,
      password,
      scaleToFit: true,
    });

    vnc.on('connect', () => {
      setStatus('connected');
      setError(null);
      console.log('VNC connected');
    });

    vnc.on('disconnect', (reason) => {
      setStatus('disconnected');
      console.log('VNC disconnected:', reason);
      onLeave();
    });

    vnc.on('reconnecting', (attempt) => {
      setStatus('reconnecting');
      setReconnectAttempt(attempt);
    });

    vnc.on('reconnected', () => {
      setStatus('connected');
      setReconnectAttempt(0);
      setError(null);
    });

    vnc.on('reconnect_failed', () => {
      setStatus('disconnected');
      setError('Failed to reconnect after multiple attempts');
    });

    vnc.on('bell', () => {
      console.log('Bell!');
    });

    setStatus('connecting');
    setError(null);

    (async () => {
      try {
        await vnc.connect();
        if (cancelled) {
          vnc.disconnect();
          return;
        }
        setClient(vnc);
      } catch (err) {
        if (!cancelled) {
          setStatus('disconnected');
          setError(err instanceof Error ? err.message : 'Connection failed');
        }
      }
    })();

    return () => {
      cancelled = true;
      vnc.disconnect();
      setClient(null);
    };
  }, [wsUrl, target, password, onLeave]);

  return (
    <>
      <Toolbar
        client={client}
        onDisconnect={onLeave}
        target={target}
        connectionStatus={toolbarStatus}
        diagnosticsOpen={showDebug}
        onToggleDiagnostics={() => setShowDebug((v) => !v)}
      />
      <div className="relative min-h-0 flex-1">
        {error && (
          <div className="absolute left-1/2 top-1/2 z-10 max-w-md -translate-x-1/2 -translate-y-1/2 rounded-lg bg-red-950/90 px-6 py-4 text-center text-red-200 shadow-xl">
            {error}
          </div>
        )}
        {status === 'reconnecting' && (
          <div className="absolute left-1/2 top-1/2 z-10 -translate-x-1/2 -translate-y-1/2 rounded-lg bg-amber-950/90 px-6 py-4 text-center text-amber-100 shadow-xl">
            Reconnecting... (attempt {reconnectAttempt})
          </div>
        )}
        {status === 'connecting' && (
          <div className="absolute left-1/2 top-1/2 z-10 -translate-x-1/2 -translate-y-1/2 rounded-lg bg-zinc-900/90 px-6 py-4 text-zinc-100 shadow-xl">
            Connecting...
          </div>
        )}
        <DebugOverlay client={client} visible={showDebug && status === 'connected'} />
        <VncViewer client={client} scaleToFit className="h-full w-full" />
      </div>
    </>
  );
}

createRoot(document.getElementById('root')!).render(<App />);
