import React, { useState } from 'react';
import { createRoot } from 'react-dom/client';
import { VncViewer } from '../src/index';
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
  const [connected, setConnected] = useState(false);
  const [showForm, setShowForm] = useState(true);

  const handleConnect = (e: React.FormEvent) => {
    e.preventDefault();
    if (!target) return;
    setShowForm(false);
    setConnected(true);
  };

  if (showForm) {
    return (
      <div style={{ maxWidth: 400, margin: '100px auto', padding: 24 }}>
        <h1 style={{ marginBottom: 24 }}>redvnc Web Client</h1>
        <form onSubmit={handleConnect} style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          <label>
            WebSocket URL
            <input
              type="text"
              value={wsUrl}
              onChange={e => setWsUrl(e.target.value)}
              style={inputStyle}
            />
          </label>
          <label>
            VNC Target (host:port)
            <input
              type="text"
              value={target}
              onChange={e => setTarget(e.target.value)}
              placeholder="192.168.1.50:5900"
              style={inputStyle}
              required
            />
          </label>
          <label>
            Password (optional)
            <input
              type="password"
              value={password}
              onChange={e => setPassword(e.target.value)}
              style={inputStyle}
            />
          </label>
          <label>
            Encoding
            <select
              value={encoding}
              onChange={e => setEncoding(e.target.value as EncodingPreset)}
              style={inputStyle}
            >
              {Object.entries(ENCODING_PRESETS).map(([key, preset]) => (
                <option key={key} value={key}>{preset.label}</option>
              ))}
            </select>
          </label>
          <button type="submit" style={buttonStyle}>Connect</button>
        </form>
      </div>
    );
  }

  return (
    <div style={{ width: '100vw', height: '100vh', display: 'flex', flexDirection: 'column' }}>
      <div style={{ padding: '8px 16px', background: '#1a1a1a', borderBottom: '1px solid #333', display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <span>Connected to {target}</span>
        <button
          onClick={() => { setConnected(false); setShowForm(true); }}
          style={buttonStyle}
        >
          Disconnect
        </button>
      </div>
      {connected && (
        <VncViewer
          url={wsUrl}
          target={target}
          password={password || undefined}
          scaleToFit
          encodings={ENCODING_PRESETS[encoding].encodings}
          onConnect={() => console.log('VNC connected')}
          onDisconnect={(reason) => {
            console.log('VNC disconnected:', reason);
            setConnected(false);
            setShowForm(true);
          }}
          onBell={() => console.log('Bell!')}
          style={{ flex: 1 }}
        />
      )}
    </div>
  );
}

const inputStyle: React.CSSProperties = {
  display: 'block',
  width: '100%',
  padding: '8px 12px',
  marginTop: 4,
  background: '#222',
  border: '1px solid #444',
  borderRadius: 4,
  color: '#eee',
  fontSize: 14,
};

const buttonStyle: React.CSSProperties = {
  padding: '8px 16px',
  background: '#0066cc',
  color: '#fff',
  border: 'none',
  borderRadius: 4,
  cursor: 'pointer',
  fontSize: 14,
};

createRoot(document.getElementById('root')!).render(<App />);
