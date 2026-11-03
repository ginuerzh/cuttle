import { BaseEdge, EdgeLabelRenderer, getBezierPath, type EdgeProps } from '@xyflow/react';

/**
 * n8n-style connection: a plain bezier with a small dot at both ends and an
 * optional label at the midpoint.
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
}: EdgeProps) {
  const [path, labelX, labelY] = getBezierPath({
    sourceX,
    sourceY,
    sourcePosition,
    targetX,
    targetY,
    targetPosition,
  });
  const color = (style?.stroke as string) || '#94a3b8';
  const radius = 3;

  return (
    <>
      <BaseEdge path={path} style={style} interactionWidth={18} />
      <circle cx={sourceX} cy={sourceY} r={radius} fill={color} stroke="#fff" strokeWidth={1} />
      <circle cx={targetX} cy={targetY} r={radius} fill={color} stroke="#fff" strokeWidth={1} />
      {label ? (
        <EdgeLabelRenderer>
          <div
            style={{
              position: 'absolute',
              transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)`,
              background: '#f8fafc',
              border: '1px solid #e2e8f0',
              borderRadius: 4,
              padding: '1px 4px',
              fontSize: 10,
              color: '#475569',
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
