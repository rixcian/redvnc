import { useEffect, useRef } from 'react';
import type { VncViewerProps } from '../types';

export const VncViewer: React.FC<VncViewerProps> = ({
  client,
  scaleToFit = false,
  className,
  style,
}) => {
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const el = canvasRef.current;
    if (!client || !el) return;
    client.attachCanvas(el);
    return () => {
      client.detachCanvas();
    };
  }, [client]);

  return (
    <div className={className} style={{ position: 'relative', ...style }}>
      <canvas
        ref={canvasRef}
        style={{
          display: 'block',
          width: scaleToFit ? '100%' : undefined,
          height: scaleToFit ? '100%' : undefined,
          objectFit: scaleToFit ? 'contain' : undefined,
          outline: 'none',
        }}
      />
    </div>
  );
};
