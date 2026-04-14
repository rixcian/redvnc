import * as React from 'react';
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from './ui/card';
import { Button } from './ui/button';
import { Input } from './ui/input';
import { Label } from './ui/label';

export interface ConnectionFormProps {
  wsUrl: string;
  target: string;
  password: string;
  onWsUrlChange: (value: string) => void;
  onTargetChange: (value: string) => void;
  onPasswordChange: (value: string) => void;
  onSubmit: (e: React.FormEvent) => void;
  children?: React.ReactNode;
}

export const ConnectionForm: React.FC<ConnectionFormProps> = ({
  wsUrl,
  target,
  password,
  onWsUrlChange,
  onTargetChange,
  onPasswordChange,
  onSubmit,
  children,
}) => (
  <div className="flex min-h-screen items-center justify-center bg-zinc-950 p-6">
    <Card className="w-full max-w-md border-zinc-800">
      <CardHeader>
        <CardTitle className="text-zinc-50">redvnc | Redamp.io VNC Client</CardTitle>
        <CardDescription>
          Connect to a VNC session through the WebSocket proxy.
        </CardDescription>
      </CardHeader>
      <form onSubmit={onSubmit}>
        <CardContent className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="ws-url">WebSocket URL</Label>
            <Input
              id="ws-url"
              type="text"
              value={wsUrl}
              onChange={(e) => onWsUrlChange(e.target.value)}
              autoComplete="off"
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="vnc-target">VNC target (host:port)</Label>
            <Input
              id="vnc-target"
              type="text"
              value={target}
              onChange={(e) => onTargetChange(e.target.value)}
              placeholder="192.168.1.50:5900"
              required
              autoComplete="off"
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="vnc-password">Password (optional)</Label>
            <Input
              id="vnc-password"
              type="password"
              value={password}
              onChange={(e) => onPasswordChange(e.target.value)}
              autoComplete="off"
            />
          </div>
          {children}
        </CardContent>
        <CardFooter>
          <Button type="submit" className="w-full">
            Connect
          </Button>
        </CardFooter>
      </form>
    </Card>
  </div>
);
