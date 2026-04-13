import { useCallback, useState } from 'react';
import { motion } from 'framer-motion';
import * as SheetPrimitive from '@radix-ui/react-dialog';
import { Sheet, SheetContent } from './ui/sheet';
import { Button } from './ui/button';
import { Switch } from './ui/switch';
import { cn } from '../lib/utils';
import { ChevronDown, X } from 'lucide-react';

export type ScaleMode = 'smart' | 'fit' | 'fill' | '1:1';
export type ImageQuality = 'low' | 'medium' | 'high' | 'max';
export type CursorMode = 'local' | 'remote' | 'both';
export type IdleTimeout = 'off' | '5' | '15' | '30' | '60';

interface SettingsPanelProps {
  visible: boolean;
  onClose: () => void;
  fixedDockTop: number;
}

function SettingsSelect<T extends string>({
  value,
  onChange,
  options,
  className,
}: {
  value: T;
  onChange: (v: T) => void;
  options: { value: T; label: string }[];
  className?: string;
}) {
  const stopKeys = useCallback((e: React.KeyboardEvent) => {
    e.stopPropagation();
  }, []);

  return (
    <div className={cn('relative min-w-[148px] shrink-0', className)}>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value as T)}
        onKeyDown={stopKeys}
        onKeyUp={stopKeys}
        className={cn(
          'h-9 w-full cursor-pointer appearance-none rounded-md border border-zinc-700 bg-zinc-900/80 py-1.5 pl-3 pr-9 text-sm text-zinc-100',
          'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500 focus-visible:ring-offset-2 focus-visible:ring-offset-zinc-950',
        )}
      >
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
      <ChevronDown
        className="pointer-events-none absolute right-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-zinc-500"
        aria-hidden
      />
    </div>
  );
}

function SettingsRow({
  title,
  description,
  control,
}: {
  title: string;
  description: string;
  control: React.ReactNode;
}) {
  return (
    <div className="flex items-center justify-between gap-4 border-b border-zinc-800 px-5 py-4">
      <div className="min-w-0 flex-1">
        <div className="text-sm font-medium text-zinc-50">{title}</div>
        <p className="mt-0.5 text-xs leading-snug text-zinc-500">{description}</p>
      </div>
      <div className="shrink-0">{control}</div>
    </div>
  );
}

export const SettingsPanel: React.FC<SettingsPanelProps> = ({
  visible,
  onClose,
  fixedDockTop,
}) => {
  const [scaleMode, setScaleMode] = useState<ScaleMode>('smart');
  const [imageQuality, setImageQuality] = useState<ImageQuality>('high');
  const [audioPassthrough, setAudioPassthrough] = useState(true);
  const [cursorMode, setCursorMode] = useState<CursorMode>('remote');
  const [keyPassthrough, setKeyPassthrough] = useState(true);
  const [relativeMouse, setRelativeMouse] = useState(false);
  const [highDpiScaling, setHighDpiScaling] = useState(true);
  const [idleTimeout, setIdleTimeout] = useState<IdleTimeout>('30');

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
          !visible && 'pointer-events-none',
        )}
        style={{
          top: fixedDockTop,
          height: `calc(100dvh - ${fixedDockTop}px)`,
          maxHeight: `calc(100dvh - ${fixedDockTop}px)`,
        }}
      >
        <SheetPrimitive.Title className="sr-only">Display Settings</SheetPrimitive.Title>
        <SheetPrimitive.Description className="sr-only">
          Session display, input, and quality options.
        </SheetPrimitive.Description>

        <motion.div
          className="flex h-full min-h-0 flex-col border-l border-zinc-800 bg-zinc-950 shadow-2xl"
          initial={false}
          animate={{ x: visible ? 0 : '100%' }}
          transition={{ type: 'tween', ease: [0.32, 0.72, 0, 1], duration: 0.32 }}
        >
          <div className="flex shrink-0 items-center justify-between border-b border-zinc-800 px-5 py-4">
            <span className="text-lg font-semibold text-zinc-50">Display Settings</span>
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

          <div className="min-h-0 flex-1 overflow-y-auto">
            <SettingsRow
              title="Scale Mode"
              description="How the remote screen fits your browser"
              control={
                <SettingsSelect
                  value={scaleMode}
                  onChange={setScaleMode}
                  options={[
                    { value: 'smart', label: 'Smart Scale' },
                    { value: 'fit', label: 'Fit' },
                    { value: 'fill', label: 'Fill' },
                    { value: '1:1', label: '1:1' },
                  ]}
                />
              }
            />
            <SettingsRow
              title="Image Quality"
              description="Higher = sharper but more bandwidth"
              control={
                <SettingsSelect
                  value={imageQuality}
                  onChange={setImageQuality}
                  options={[
                    { value: 'low', label: 'Low' },
                    { value: 'medium', label: 'Medium' },
                    { value: 'high', label: 'High' },
                    { value: 'max', label: 'Max' },
                  ]}
                />
              }
            />
            <SettingsRow
              title="Audio Passthrough"
              description="Stream remote audio to local"
              control={
                <Switch checked={audioPassthrough} onCheckedChange={setAudioPassthrough} />
              }
            />
            <SettingsRow
              title="Cursor Mode"
              description="Show local, remote, or both cursors"
              control={
                <SettingsSelect
                  value={cursorMode}
                  onChange={setCursorMode}
                  options={[
                    { value: 'local', label: 'Local Only' },
                    { value: 'remote', label: 'Remote Only' },
                    { value: 'both', label: 'Both' },
                  ]}
                />
              }
            />
            <SettingsRow
              title="Key Passthrough"
              description="Send all keyboard input to remote"
              control={<Switch checked={keyPassthrough} onCheckedChange={setKeyPassthrough} />}
            />
            <SettingsRow
              title="Relative Mouse"
              description="Better for games & 3D apps"
              control={<Switch checked={relativeMouse} onCheckedChange={setRelativeMouse} />}
            />
            <SettingsRow
              title="High DPI Scaling"
              description="Match retina / HiDPI displays"
              control={
                <Switch checked={highDpiScaling} onCheckedChange={setHighDpiScaling} />
              }
            />
            <SettingsRow
              title="Idle Timeout"
              description="Auto-disconnect after inactivity"
              control={
                <SettingsSelect
                  value={idleTimeout}
                  onChange={setIdleTimeout}
                  options={[
                    { value: 'off', label: 'Off' },
                    { value: '5', label: '5 min' },
                    { value: '15', label: '15 min' },
                    { value: '30', label: '30 min' },
                    { value: '60', label: '60 min' },
                  ]}
                />
              }
            />
          </div>
        </motion.div>
      </SheetContent>
    </Sheet>
  );
};
