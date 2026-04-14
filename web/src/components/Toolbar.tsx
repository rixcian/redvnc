import { useCallback, useLayoutEffect, useRef, useState } from 'react';
import type { VncClient } from '../index';
import { ClipboardPanel } from './ClipboardPanel';
import { SettingsPanel } from './SettingsPanel';
import { Button } from './ui/button';
import { Badge } from './ui/badge';
import { Separator } from './ui/separator';
import { cn } from '../lib/utils';
import {
  Activity,
  BarChart3,
  ClipboardList,
  FolderOpen,
  HelpCircle,
  Keyboard,
  Maximize2,
  Monitor,
  Power,
  Settings,
  Terminal,
  Wrench,
} from 'lucide-react';

export type ToolbarConnectionStatus =
  | 'idle'
  | 'connecting'
  | 'connected'
  | 'reconnecting'
  | 'error';

interface ToolbarProps {
  client: VncClient | null;
  onDisconnect?: () => void;
  /** VNC target host:port — drives the status line when title/subtitle omitted. */
  target?: string;
  /** Override primary session label (e.g. hostname). */
  sessionTitle?: string;
  /** Override secondary label (e.g. port or IP). */
  sessionSubtitle?: string;
  connectionStatus?: ToolbarConnectionStatus;
  /** Toggle connection stats / debug overlay (Activity icon). */
  onToggleDiagnostics?: () => void;
  /** When true, Activity button shows selected state. */
  diagnosticsOpen?: boolean;
  className?: string;
  style?: React.CSSProperties;
}

function parseTargetParts(target: string): { title: string; subtitle: string } {
  const t = target.trim();
  if (!t) return { title: '—', subtitle: '' };
  const colon = t.indexOf(':');
  if (colon <= 0) return { title: t, subtitle: '' };
  return {
    title: t.slice(0, colon),
    subtitle: t.slice(colon + 1),
  };
}

export const Toolbar: React.FC<ToolbarProps> = ({
  client,
  className,
  style,
  onDisconnect,
  target,
  sessionTitle,
  sessionSubtitle,
  connectionStatus = 'idle',
  onToggleDiagnostics,
  diagnosticsOpen = false,
}) => {
  const fileInputRef = useRef<HTMLInputElement>(null);
  const barRef = useRef<HTMLDivElement>(null);
  const [showClipboard, setShowClipboard] = useState(false);
  const [showSettings, setShowSettings] = useState(false);
  const [panelDockTop, setPanelDockTop] = useState(0);
  const [activeDisplay, setActiveDisplay] = useState<1 | 2>(1);

  useLayoutEffect(() => {
    const el = barRef.current;
    if (!el) return;
    const sync = () => setPanelDockTop(el.getBoundingClientRect().bottom);
    sync();
    const ro = new ResizeObserver(sync);
    ro.observe(el);
    window.addEventListener('resize', sync);
    return () => {
      ro.disconnect();
      window.removeEventListener('resize', sync);
    };
  }, []);

  const handleFileUpload = useCallback(() => {
    fileInputRef.current?.click();
  }, []);

  const handleFileChange = useCallback(
    async (e: React.ChangeEvent<HTMLInputElement>) => {
      if (!client?.connected || !e.target.files) return;
      for (const file of Array.from(e.target.files)) {
        await client.uploadFile(file);
      }
      e.target.value = '';
    },
    [client],
  );

  const handleFullscreen = useCallback(() => {
    const el = document.querySelector('canvas');
    el?.requestFullscreen?.();
  }, []);

  const handleSendKeysFocus = useCallback(() => {
    document.querySelector('canvas')?.focus();
  }, []);

  const handleDisconnect = useCallback(() => {
    client?.disconnect();
    onDisconnect?.();
  }, [client, onDisconnect]);

  const targetLine =
    sessionTitle !== undefined || sessionSubtitle !== undefined
      ? { title: sessionTitle ?? '—', subtitle: sessionSubtitle ?? '' }
      : parseTargetParts(target ?? '');

  const statusBadge = (() => {
    switch (connectionStatus) {
      case 'connected':
        return (
          <Badge variant="success" className="shrink-0 text-[10px] uppercase tracking-wider">
            CONNECTED
          </Badge>
        );
      case 'connecting':
        return (
          <Badge variant="warning" className="shrink-0 text-[10px] uppercase tracking-wider">
            CONNECTING
          </Badge>
        );
      case 'reconnecting':
        return (
          <Badge variant="warning" className="shrink-0 text-[10px] uppercase tracking-wider">
            RECONNECTING
          </Badge>
        );
      case 'error':
        return (
          <Badge variant="destructive" className="shrink-0 text-[10px] uppercase tracking-wider">
            ERROR
          </Badge>
        );
      default:
        return (
          <Badge variant="outline" className="shrink-0 text-[10px] uppercase tracking-wider">
            IDLE
          </Badge>
        );
    }
  })();

  return (
    <div
      ref={barRef}
      className={cn(
        'flex flex-col shrink-0 border-b border-zinc-800 bg-zinc-950 text-zinc-100',
        className,
      )}
      style={style}
    >
      <div className="flex min-h-10 min-w-0 items-center gap-3 px-3 py-2">
        {statusBadge}
        <div className="min-w-0 truncate text-sm text-zinc-400">
          <span className="font-medium text-zinc-200">{targetLine.title}</span>
          {targetLine.subtitle ? (
            <>
              <span className="text-zinc-600"> · </span>
              <span>{targetLine.subtitle}</span>
            </>
          ) : null}
        </div>

        <Separator orientation="vertical" className="h-7" />

        <div className="flex shrink-0 gap-1">
          <Button
            type="button"
            variant={activeDisplay === 1 ? 'secondary' : 'outline'}
            size="sm"
            className="gap-1.5 border-zinc-700"
            onClick={() => setActiveDisplay(1)}
          >
            <Monitor className="h-3.5 w-3.5" />
            Display 1
          </Button>
        </div>

        <div className="flex-1" />

        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="gap-1.5 text-zinc-300"
          onClick={handleSendKeysFocus}
        >
          <Keyboard className="h-4 w-4" />
          Send Keys
        </Button>
        <Button
          type="button"
          variant={showClipboard ? 'secondary' : 'ghost'}
          size="sm"
          className="gap-1.5"
          onClick={() => {
            setShowClipboard((v) => !v);
            setShowSettings(false);
          }}
        >
          <ClipboardList className="h-4 w-4" />
          Clipboard
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="gap-1.5 text-zinc-300"
          onClick={handleFileUpload}
        >
          <FolderOpen className="h-4 w-4" />
          Files
        </Button>

        <Separator orientation="vertical" className="h-7" />

        <Button
          type="button"
          variant={showSettings ? 'secondary' : 'ghost'}
          size="icon"
          className={showSettings ? 'text-blue-300' : 'text-zinc-500'}
          title="Settings"
          onClick={() => {
            setShowSettings((v) => !v);
            setShowClipboard(false);
          }}
        >
          <Settings className="h-4 w-4" />
        </Button>
        <Button
          type="button"
          variant={diagnosticsOpen ? 'secondary' : 'ghost'}
          size="icon"
          className={diagnosticsOpen ? 'text-blue-300' : 'text-zinc-500'}
          title="Diagnostics"
          onClick={() => onToggleDiagnostics?.()}
          disabled={!onToggleDiagnostics || connectionStatus !== 'connected'}
        >
          <Activity className="h-4 w-4" />
        </Button>
        <Button type="button" variant="ghost" size="icon" className="text-zinc-500" title="Help">
          <HelpCircle className="h-4 w-4" />
        </Button>

        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="text-zinc-400"
          title="Fullscreen"
          onClick={handleFullscreen}
        >
          <Maximize2 className="h-4 w-4" />
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="text-red-400 hover:bg-red-950/50 hover:text-red-300"
          title="Disconnect"
          onClick={handleDisconnect}
        >
          <Power className="h-4 w-4" />
        </Button>
      </div>

      <input
        ref={fileInputRef}
        type="file"
        multiple
        className="hidden"
        onChange={handleFileChange}
      />
      <ClipboardPanel
        client={client}
        visible={showClipboard}
        onClose={() => setShowClipboard(false)}
        fixedDockTop={panelDockTop}
      />
      <SettingsPanel
        visible={showSettings}
        onClose={() => setShowSettings(false)}
        fixedDockTop={panelDockTop}
      />
    </div>
  );
};

export { parseTargetParts };
