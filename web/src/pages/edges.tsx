import { BaseEdge, EdgeLabelRenderer, getBezierPath, type EdgeProps } from '@xyflow/react';

/**
 * n8n-style connection: a plain bezier with an optional label at the midpoint.
 * The endpoint dots come from the node handles; drawing them here as well
 * would render two dots at every connection point.
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
  return (
    <>
      <BaseEdge path={path} style={edgeStyle} interactionWidth={18} />
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
