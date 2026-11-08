import { useEffect, useRef } from 'react';
import { BaseEdge, EdgeLabelRenderer, getBezierPath, type EdgeProps } from '@xyflow/react';

interface FlowData extends Record<string, unknown> {
  pulse?: number;
  error?: boolean;
  duration?: number;
}

const DOTS = 3;
const TRAIL = 0.12;

/**
 * n8n-style connection: a plain bezier with an optional label at the midpoint.
 * The endpoint dots come from the node handles; drawing them here as well
 * would render two dots at every connection point.
 *
 * A snapshot with new traffic starts a one-shot rAF animation: three light
 * dots follow the exact SVG path and fade out at the end. No SMIL and no loop.
 */
export function DotEdge({
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  style,
  label,
  selected,
  data,
}: EdgeProps) {
  const [path, labelX, labelY] = getBezierPath({
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
  });
  const edgeStyle = selected
    ? { ...style, stroke: '#0ea5e9', strokeWidth: 3.5 }
    : style;

  const flow = data as FlowData | undefined;
  const pulse = flow?.pulse ?? 0;
  const duration = Math.max(0.3, flow?.duration || 0.9);
  const dotColor = flow?.error ? '#dc2626' : '#0ea5e9';
  const pathRef = useRef<SVGPathElement | null>(null);
  const dotRefs = useRef<Array<SVGGElement | null>>([]);

  useEffect(() => {
    if (!pulse) return;
    if (window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches) return;

    const pathEl = pathRef.current;
    if (!pathEl || typeof pathEl.getTotalLength !== 'function') return;
    const total = pathEl.getTotalLength();
    if (!total) return;

    const durationMs = duration * 1000;
    const start = performance.now();
    let frame = 0;

    const tick = (now: number) => {
      const elapsed = (now - start) / durationMs;
      dotRefs.current.forEach((dot, index) => {
        if (!dot) return;
        const progress = elapsed - index * TRAIL;
        if (progress <= 0 || progress >= 1) {
          dot.setAttribute('opacity', '0');
          return;
        }
        const point = pathEl.getPointAtLength(progress * total);
        dot.setAttribute('transform', `translate(${point.x} ${point.y})`);
        const fadeIn = progress < 0.1 ? progress / 0.1 : 1;
        const fadeOut = progress > 0.85 ? (1 - progress) / 0.15 : 1;
        dot.setAttribute('opacity', String(Math.min(fadeIn, fadeOut)));
      });

      if (elapsed < 1) {
        frame = requestAnimationFrame(tick);
      } else {
        dotRefs.current.forEach((dot) => dot?.setAttribute('opacity', '0'));
      }
    };

    frame = requestAnimationFrame(tick);
    return () => {
      cancelAnimationFrame(frame);
      dotRefs.current.forEach((dot) => dot?.setAttribute('opacity', '0'));
    };
  }, [pulse, duration, path]);

  return (
    <>
      <BaseEdge path={path} style={edgeStyle} interactionWidth={18} />
      <path ref={pathRef} d={path} fill="none" stroke="none" />
      {pulse > 0
        ? Array.from({ length: DOTS }, (_, index) => (
            <g
              key={`${pulse}-${index}`}
              ref={(el) => {
                dotRefs.current[index] = el;
              }}
              className="gw-edge-dot"
              opacity={0}
              pointerEvents="none"
              style={{ filter: `drop-shadow(0 0 5px ${dotColor})` }}
            >
              <circle r={7 - index * 0.8} fill={dotColor} opacity="0.22" />
              <circle r={4 - index * 0.6} fill="#fff" opacity="0.95" />
            </g>
          ))
        : null}
      {label ? (
        <EdgeLabelRenderer>
          <div
            style={{
              position: 'absolute',
              transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)`,
              background: 'var(--gw-edge-label-bg)',
              border: `1px solid ${selected ? '#0ea5e9' : 'var(--gw-border)'}`,
              borderRadius: 4,
              padding: '1px 4px',
              fontSize: 10,
              color: selected ? '#0284c7' : 'var(--gw-text-muted)',
              fontWeight: selected ? 600 : undefined,
              pointerEvents: 'none',
              whiteSpace: 'nowrap',
            }}
          >
            {label}
          </div>
        </EdgeLabelRenderer>
      ) : null}
    </>
  );
}

export const edgeTypes = { dot: DotEdge };
