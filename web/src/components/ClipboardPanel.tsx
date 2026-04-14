import { useState, useEffect, useCallback, useRef } from 'react';
import { motion } from 'framer-motion';
import * as SheetPrimitive from '@radix-ui/react-dialog';
import type { VncClient } from '../index';
import type { ClipboardEntry } from '../types';
import { Sheet, SheetContent } from './ui/sheet';
import { Button } from './ui/button';
import { Switch } from './ui/switch';
import { Textarea } from './ui/textarea';
import { Label } from './ui/label';
import { cn } from '../lib/utils';
import { X } from 'lucide-react';

interface ClipboardPanelProps {
  client: VncClient | null;
  visible: boolean;
  onClose: () => void;
  /** Viewport Y where the docked panel starts (e.g. bottom edge of the toolbar). */
  fixedDockTop: number;
}

function timeAgo(timestamp: number): string {
  const seconds = Math.floor((Date.now() - timestamp) / 1000);
  if (seconds < 60) return 'just now';
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.floor(minutes / 60);
  return `${hours}h ago`;
}

export const ClipboardPanel: React.FC<ClipboardPanelProps> = ({
  client,
  visible,
  onClose,
  fixedDockTop,
}) => {
  const [history, setHistory] = useState<ClipboardEntry[]>([]);
  const [manualText, setManualText] = useState('');
  const [, setTick] = useState(0);
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    if (!client) return;
    setHistory(client.getClipboardHistory());
    client.onClipboardHistory((entries) => setHistory(entries));
  }, [client]);

  useEffect(() => {
    if (!visible) return;
    const id = setInterval(() => setTick((t) => t + 1), 30_000);
    return () => clearInterval(id);
  }, [visible]);

  const handlePasteToRemote = useCallback(
    (text: string) => {
      client?.sendClipboard(text);
    },
    [client],
  );

  const handleCopyToLocal = useCallback((text: string) => {
    if (!navigator.clipboard?.writeText) return;
    void navigator.clipboard.writeText(text).catch(() => {
      /* ignore — permission / insecure context */
    });
  }, []);

  const handleManualSend = useCallback(() => {
    if (!client || !manualText.trim()) return;
    client.sendClipboard(manualText);
    setManualText('');
  }, [client, manualText]);

  const handleTextareaKeyDown = useCallback((e: React.KeyboardEvent) => {
    e.stopPropagation();
  }, []);

  return (
    <Sheet
      open={visible}
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
      modal={false}
    >
      <SheetContent
        forceMount
        hideOverlay
        disableBuiltInSlide
        className={cn(
          'w-[380px] gap-0 overflow-hidden border-0 bg-transparent p-0 shadow-none',
          // forceMount keeps a fixed hit target on the right; disable hits while closed so the canvas receives clicks
          !visible && 'pointer-events-none',
        )}
        style={{
          top: fixedDockTop,
          height: `calc(100dvh - ${fixedDockTop}px)`,
          maxHeight: `calc(100dvh - ${fixedDockTop}px)`,
        }}
      >
        <SheetPrimitive.Title className="sr-only">Clipboard Sync</SheetPrimitive.Title>
        <SheetPrimitive.Description className="sr-only">
          Clipboard history and manual paste to the remote session.
        </SheetPrimitive.Description>

        <motion.div
          className="flex h-full min-h-0 flex-col border-l border-zinc-800 bg-zinc-950 shadow-2xl"
          initial={false}
          animate={{ x: visible ? 0 : '100%' }}
          transition={{ type: 'tween', ease: [0.32, 0.72, 0, 1], duration: 0.32 }}
        >
          <div className="flex shrink-0 items-center justify-between border-b border-zinc-800 px-5 py-4">
            <span className="text-lg font-semibold text-zinc-50">Clipboard History</span>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              className="h-8 w-8 text-zinc-500 hover:text-zinc-200"
              onClick={onClose}
            >
              <X className="h-5 w-5" />
            </Button>
          </div>

          <div className="min-h-0 flex-1 overflow-y-auto px-5 py-3">
            <div className="mb-2 text-[11px] font-semibold uppercase tracking-wider text-zinc-500">
              History
            </div>
            {history.length === 0 && (
              <p className="py-3 text-sm italic text-zinc-600">
                No clipboard activity yet.
              </p>
            )}
            {history.map((entry, index) => (
              <div
                key={entry.id}
                className={cn(
                  'mb-2 rounded-lg border p-3',
                  index === 0
                    ? 'border-blue-500/40 bg-blue-500/10'
                    : 'border-zinc-800 bg-zinc-900/60',
                )}
              >
                <div
                  className={cn(
                    'max-h-[60px] overflow-hidden text-sm leading-snug whitespace-pre-wrap break-all',
                    index === 0 ? 'text-blue-100' : 'text-zinc-400',
                  )}
                >
                  {entry.text.length > 120 ? `${entry.text.slice(0, 120)}…` : entry.text}
                </div>
                <div className="mt-2 flex items-center justify-between gap-2">
                  <span className="text-[11px] text-zinc-600">
                    {timeAgo(entry.timestamp)} ·{' '}
                    {entry.source === 'local' ? 'Local -> Remote' : 'Remote -> Local'}
                  </span>
                  {entry.source === 'remote' ? (
                    <Button
                      type="button"
                      variant="link"
                      className="h-auto p-0 text-xs font-semibold text-blue-400"
                      onClick={() => handleCopyToLocal(entry.text)}
                    >
                      Copy
                    </Button>
                  ) : (
                    <Button
                      type="button"
                      variant="link"
                      className="h-auto p-0 text-xs font-semibold text-blue-400"
                      onClick={() => handlePasteToRemote(entry.text)}
                    >
                      Paste
                    </Button>
                  )}
                </div>
              </div>
            ))}
          </div>

          <div className="shrink-0 border-t border-zinc-800 px-5 py-4">
            <div className="mb-1 font-semibold text-zinc-50">Manual paste</div>
            <p className="mb-3 text-xs text-zinc-500">
              Type or paste text here and click &quot;Send&quot; to push directly to the remote
              clipboard.
            </p>
            <Textarea
              ref={textareaRef}
              value={manualText}
              onChange={(e) => setManualText(e.target.value)}
              onKeyDown={handleTextareaKeyDown}
              onKeyUp={(e) => e.stopPropagation()}
              placeholder="Paste content here..."
              className="min-h-[64px] resize-none font-mono text-sm"
            />
            <Button
              type="button"
              className="mt-3 w-full"
              disabled={!manualText.trim()}
              onClick={handleManualSend}
            >
              Send to Remote
            </Button>
          </div>
        </motion.div>
      </SheetContent>
    </Sheet>
  );
};
