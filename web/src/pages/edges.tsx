import { createElement } from 'react';
import { BaseEdge, EdgeLabelRenderer, getBezierPath, type EdgeProps } from '@xyflow/react';

interface FlowData extends Record<string, unknown> {
  pulse?: number;
  error?: boolean;
  duration?: number;
}

/**
 * n8n-style connection: a plain bezier with an optional label at the midpoint.
 * The endpoint dots come from the node handles; drawing them here as well
 * would render two dots at every connection point.
 *
 * When a snapshot reports new traffic, one light dot traverses the edge once.
 * The dot is keyed by the pulse id so a new snapshot restarts the animation
 * without any infinite looping.
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
    sourcePosition,
    targetX,
    targetY,
    targetPosition,
  });
  const edgeStyle = selected
    ? { ...style, stroke: '#0ea5e9', strokeWidth: 3.5 }
    : style;

  const flow = data as FlowData | undefined;
  const pulse = flow?.pulse ?? 0;
  const duration = Math.max(0.3, flow?.duration || 0.9);
  const dotColor = flow?.error ? '#dc2626' : ((style?.stroke as string) || '#0ea5e9');

  return (
    <>
      <BaseEdge path={path} style={edgeStyle} interactionWidth={18} />
      {pulse > 0 ? (
        <g
          key={pulse}
          opacity={0}
          pointerEvents="none"
          style={{ filter: `drop-shadow(0 0 4px ${dotColor})` }}
        >
          {createElement('animateMotion', { path, dur: `${duration}s`, fill: 'remove' })}
          <animate
            attributeName="opacity"
            values="0;1;1;0"
            keyTimes="0;0.1;0.85;1"
            dur={`${duration}s`}
            fill="remove"
          />
          <circle r="4.5" fill={dotColor} opacity="0.25" />
          <circle r="2.5" fill={dotColor} />
        </g>
      ) : null}
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
