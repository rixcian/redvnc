import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { createRoot } from 'react-dom/client';
import {
  VncClient,
  VncViewer,
  DebugOverlay,
  Toolbar,
  ConnectionForm,
} from '../src/index';
import {
  EncodingRaw,
  EncodingCopyRect,
  EncodingZlib,
  EncodingTight,
  EncodingZRLE,
  EncodingH264,
  EncodingCursor,
  EncodingDesktopSize,
} from '../src/types';

type EncodingPreset = 'auto' | 'h264' | 'tight' | 'zrle' | 'zlib' | 'raw';

const ENCODING_PRESETS: Record<EncodingPreset, { label: string; encodings: number[] }> = {
  auto: {
    label: 'Auto (H.264 > Tight > ZRLE)',
    encodings: [EncodingH264, EncodingTight, EncodingZRLE, EncodingZlib, EncodingCopyRect, EncodingRaw, EncodingCursor, EncodingDesktopSize],
  },
  h264: {
    label: 'H.264',
    encodings: [EncodingH264, EncodingCopyRect, EncodingRaw, EncodingCursor, EncodingDesktopSize],
  },
  tight: {
    label: 'Tight (JPEG tiles)',
    encodings: [EncodingTight, EncodingCopyRect, EncodingRaw, EncodingCursor, EncodingDesktopSize],
  },
  zrle: {
    label: 'ZRLE',
    encodings: [EncodingZRLE, EncodingCopyRect, EncodingRaw, EncodingCursor, EncodingDesktopSize],
  },
  zlib: {
    label: 'Zlib',
    encodings: [EncodingZlib, EncodingCopyRect, EncodingRaw, EncodingCursor, EncodingDesktopSize],
  },
  raw: {
    label: 'Raw (uncompressed)',
    encodings: [EncodingCopyRect, EncodingRaw, EncodingCursor, EncodingDesktopSize],
  },
};

function App() {
  const [wsUrl, setWsUrl] = useState('ws://localhost:8080/ws');
  const [target, setTarget] = useState('');
  const [password, setPassword] = useState('');
  const [encoding, setEncoding] = useState<EncodingPreset>('auto');
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
      >
        <div className="space-y-2">
          <label htmlFor="encoding" className="text-sm font-medium leading-none text-zinc-200">
            Encoding
          </label>
          <select
            id="encoding"
            value={encoding}
            onChange={e => setEncoding(e.target.value as EncodingPreset)}
            className="flex h-10 w-full rounded-md border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm text-zinc-50 ring-offset-zinc-950 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-zinc-400 focus-visible:ring-offset-2"
          >
            {Object.entries(ENCODING_PRESETS).map(([key, preset]) => (
              <option key={key} value={key}>{preset.label}</option>
            ))}
          </select>
        </div>
      </ConnectionForm>
    );
  }

  return (
    <div className="flex h-screen w-screen flex-col bg-zinc-950">
      <VncSession
        wsUrl={wsUrl}
        target={target}
        password={password || undefined}
        encodings={ENCODING_PRESETS[encoding].encodings}
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
  encodings,
  onLeave,
}: {
  wsUrl: string;
  target: string;
  password?: string;
  encodings?: number[];
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
      encodings,
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
  }, [wsUrl, target, password, encodings, onLeave]);

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
