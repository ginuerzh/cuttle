import { Handle, Position, type NodeProps } from '@xyflow/react';
import {
  ApiOutlined,
  BranchesOutlined,
  CloudServerOutlined,
  FrownOutlined,
  GatewayOutlined,
  MehOutlined,
  SmileOutlined,
  WarningOutlined,
} from '@ant-design/icons';
import type { NodeMetrics } from '../api/types';
import type { GraphNode } from './build';
import { translate as t } from '../i18n';
import { formatDurationMs, formatTokenRate } from '../time';

function errorRate(m?: NodeMetrics): number {
  if (!m || m.requests === 0) return 0;
  return m.errors / m.requests;
}

/**
 * Four visible node states share one badge slot: dead, unhealthy, degraded and
 * healthy. A node with no traffic keeps the neutral border and shows no badge,
 * because it has no health signal yet.
 */
export type NodeMood = 'dead' | 'error' | 'warn' | 'healthy' | 'idle';

export function nodeMood(m?: NodeMetrics, dead = false): NodeMood {
  if (dead) return 'dead';
  if (!m || m.requests === 0) return 'idle';
  const r = errorRate(m);
  if (r > 0.05) return 'error';
  if (r >= 0.01) return 'warn';
  return 'healthy';
}

function healthColor(m?: NodeMetrics): string {
  if (!m || m.requests === 0) return '#94a3b8';
  const r = errorRate(m);
  if (r > 0.05) return '#dc2626';
  if (r >= 0.01) return '#d97706';
  return '#16a34a';
}

function fmt(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
  return `${n}`;
}

function errorPercent(m: NodeMetrics): string {
  const err = errorRate(m) * 100;
  return `${err < 10 ? err.toFixed(1) : err.toFixed(0)}%`;
}

function metricsText(m: NodeMetrics, showRate: boolean): string {
  if (m.requests === 0) return t('暂无会话');
  const error = m.errors > 0 ? ` · ${errorPercent(m)}` : '';
  const rate = showRate ? formatTokenRate(m.outputTokensPerSecond) : '';
  const speed = rate ? ` · ${rate}` : '';
  return `${fmt(m.requests)}${error} · p95 ${formatDurationMs(m.p95Ms)}${speed}`;
}

const box: React.CSSProperties = {
  position: 'relative',
  display: 'flex',
  alignItems: 'center',
  gap: 10,
  borderRadius: 10,
  border: '1.5px solid',
  padding: '10px 12px',
  background: 'var(--gw-surface)',
  fontSize: 12,
  color: 'var(--gw-text)',
  boxShadow: 'var(--gw-node-shadow)',
};

export function selectionStyle(selected: boolean | undefined, color: string) {
  return {
    borderColor: selected ? '#0ea5e9' : color,
    // Draw the selected outline inside the box so the node size, and therefore
    // its handles and edges, never moves when selection changes.
    boxShadow: selected
      ? 'inset 0 0 0 2px #0ea5e9, var(--gw-node-shadow)'
      : 'var(--gw-node-shadow)',
  };
}

interface IconBadgeProps {
  color: string;
  children: React.ReactNode;
  pulse?: number;
  error?: boolean;
}

function IconBadge({ color, children, pulse, error }: IconBadgeProps) {
  const className = pulse
    ? error
      ? 'gw-node-flash gw-node-flash-error'
      : 'gw-node-flash'
    : undefined;
  return (
    <div
      key={pulse || 0}
      className={className}
      style={{
        width: 34,
        height: 34,
        flex: 'none',
        borderRadius: 9,
        background: color,
        color: '#fff',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        fontSize: 17,
      }}
    >
      {children}
    </div>
  );
}

/** The dead face shown in the Flaticon "dead" icon: crossed eyes and a frown. */
function DeadFaceIcon() {
  return (
    <svg viewBox="0 0 64 64" width="16" height="16" aria-hidden="true" focusable="false">
      <g fill="none" stroke="currentColor" strokeWidth={4} strokeLinecap="round">
        <circle cx="32" cy="32" r="28" />
        <path d="M19 21l8 8M27 21l-8 8" />
        <path d="M37 21l8 8M45 21l-8 8" />
        <path d="M21 43q11-10 22 0" />
      </g>
    </svg>
  );
}

const MOOD_STYLE: Record<Exclude<NodeMood, 'idle'>, { color: string; label: string }> = {
  dead: { color: '#dc2626', label: 'dead' },
  error: { color: '#dc2626', label: '错误' },
  warn: { color: '#d97706', label: '警告' },
  healthy: { color: '#16a34a', label: '正常' },
};

/**
 * Health badge pinned inside the card's top-right corner. It is absolutely
 * positioned and pointer-transparent so it never changes the node size or
 * blocks node selection and dragging.
 */
export function NodeMoodBadge({ mood }: { mood: NodeMood }) {
  if (mood === 'idle') return null;
  const { color, label } = MOOD_STYLE[mood];
  return (
    <span
      data-status={mood}
      role="img"
      aria-label={t(label)}
      title={t(label)}
      style={{
        position: 'absolute',
        top: 4,
        right: 4,
        width: 22,
        height: 22,
        borderRadius: '50%',
        background: '#fff',
        border: `1px solid ${color}`,
        color,
        fontSize: 14,
        lineHeight: 1,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        boxShadow: '0 1px 2px rgba(15, 23, 42, 0.2)',
        pointerEvents: 'none',
        zIndex: 1,
      }}
    >
      {mood === 'dead' ? (
        <DeadFaceIcon />
      ) : mood === 'error' ? (
        <FrownOutlined />
      ) : mood === 'warn' ? (
        <MehOutlined />
      ) : (
        <SmileOutlined />
      )}
    </span>
  );
}

const HANDLE_SIZE = 14;

/** The endpoint dot size; also the connection start target in edit mode. */
export function handleStyle(color: string): React.CSSProperties {
  return {
    width: HANDLE_SIZE,
    height: HANDLE_SIZE,
    background: color,
  };
}

export function NodeBody({ label, sub }: { label: string; sub?: string }) {
  return (
    <div style={{ minWidth: 0 }}>
      <div style={{ fontWeight: 600, fontSize: 13, whiteSpace: 'nowrap' }}>{label}</div>
      {sub ? <div style={{ color: 'var(--gw-node-text-muted)', fontSize: 11, marginTop: 2 }}>{sub}</div> : null}
    </div>
  );
}

/**
 * Metrics live outside the card flow so they never resize the node. They only
 * appear in monitor mode; keeping them absolute means switching modes cannot
 * move the node box, its handles or its edges.
 */
export function NodeStats({ metrics, showRate = false }: { metrics?: NodeMetrics; showRate?: boolean }) {
  if (!metrics) return null;
  const text = metricsText(metrics, showRate);
  return (
    <div
      title={text}
      style={{
        position: 'absolute',
        top: '100%',
        left: 0,
        right: 0,
        marginTop: 4,
        fontSize: 11,
        lineHeight: '15px',
        color: 'var(--gw-text-metrics)',
        whiteSpace: 'nowrap',
        overflow: 'hidden',
        textOverflow: 'ellipsis',
        textAlign: 'center',
        pointerEvents: 'none',
      }}
    >
      {metrics.requests === 0 ? (
        text
      ) : (
        <>
          {`${fmt(metrics.requests)}`}
          {metrics.errors > 0 ? (
            <>
              {' · '}
              <span style={{ color: '#dc2626' }}>{errorPercent(metrics)}</span>
            </>
          ) : null}
          {` · p95 ${formatDurationMs(metrics.p95Ms)}`}
          {showRate && metrics.outputTokensPerSecond > 0 ? (
            <>{` · ${formatTokenRate(metrics.outputTokensPerSecond)}`}</>
          ) : null}
        </>
      )}
    </div>
  );
}

export function EntrypointNode({ data, selected, isConnectable }: NodeProps<GraphNode>) {
  return (
    <div
      style={{
        ...box,
        ...selectionStyle(selected, '#6366f1'),
        background: 'var(--gw-node-entrypoint-bg)',
        minWidth: 220,
        paddingRight: 30,
      }}
    >
      <IconBadge color="#6366f1" pulse={data.pulse} error={data.error}>
        <GatewayOutlined />
      </IconBadge>
      <NodeBody label={data.label} sub={data.sub} />
      <NodeStats metrics={data.metrics} />
      <NodeMoodBadge mood={nodeMood(data.metrics)} />
      <Handle type="source" position={Position.Right} isConnectable={isConnectable} style={handleStyle('#6366f1')} />
    </div>
  );
}

export function RouteNode({ data, selected, isConnectable }: NodeProps<GraphNode>) {
  const color = healthColor(data.metrics);
  return (
    <div
      style={{
        ...box,
        ...selectionStyle(selected, color),
        background: 'var(--gw-node-route-bg)',
        minWidth: 250,
        paddingRight: 30,
      }}
    >
      <IconBadge color="#0ea5e9" pulse={data.pulse} error={data.error}>
        <BranchesOutlined />
      </IconBadge>
      <NodeBody label={data.label} sub={data.sub} />
      <NodeStats metrics={data.metrics} />
      <NodeMoodBadge mood={nodeMood(data.metrics)} />
      <Handle type="source" position={Position.Right} isConnectable={isConnectable} style={handleStyle('#0ea5e9')} />
      <Handle type="target" position={Position.Left} isConnectable={isConnectable} style={handleStyle('#0ea5e9')} />
    </div>
  );
}

export function BindingNode({ data, selected, isConnectable }: NodeProps<GraphNode>) {
  const color = data.dead ? '#dc2626' : healthColor(data.metrics);
  return (
    <div
      style={{
        ...box,
        ...selectionStyle(selected, color),
        borderStyle: data.node?.backup ? 'dashed' : 'solid',
        background: data.disabled ? 'var(--gw-node-disabled-bg)' : 'var(--gw-node-binding-bg)',
        opacity: data.disabled ? 0.55 : 1,
        minWidth: 220,
        paddingRight: 30,
      }}
      title={data.dead ? 'dead' : undefined}
    >
      <IconBadge
        color={data.dead ? 'var(--gw-text-muted)' : '#0891b2'}
        pulse={data.pulse}
        error={data.error}
      >
        <ApiOutlined />
      </IconBadge>
      <NodeBody label={data.label} sub={data.sub} />
      <NodeStats metrics={data.metrics} showRate />
      <NodeMoodBadge mood={nodeMood(data.metrics, data.dead)} />
      <Handle type="target" position={Position.Left} isConnectable={isConnectable} style={handleStyle('#0891b2')} />
      <Handle type="source" position={Position.Right} isConnectable={isConnectable} style={handleStyle('#0891b2')} />
    </div>
  );
}

export function ProviderNode({ data, selected, isConnectable }: NodeProps<GraphNode>) {
  const color = healthColor(data.metrics);
  return (
    <div
      style={{
        ...box,
        ...selectionStyle(selected, color),
        background: data.disabled ? 'var(--gw-node-disabled-bg)' : 'var(--gw-node-provider-bg)',
        opacity: data.disabled ? 0.55 : 1,
        minWidth: 230,
        paddingRight: 30,
      }}
    >
      <IconBadge color="#10b981" pulse={data.pulse} error={data.error}>
        <CloudServerOutlined />
      </IconBadge>
      <NodeBody label={data.label} sub={data.sub} />
      <NodeStats metrics={data.metrics} />
      <NodeMoodBadge mood={nodeMood(data.metrics)} />
      <Handle type="target" position={Position.Left} isConnectable={isConnectable} style={handleStyle('#10b981')} />
    </div>
  );
}

export function UnroutedNode({ data, selected, isConnectable }: NodeProps<GraphNode>) {
  return (
    <div style={{ ...box, ...selectionStyle(selected, '#dc2626'), background: 'var(--gw-node-unrouted-bg)', minWidth: 190 }}>
      <IconBadge color="#dc2626" pulse={data.pulse} error={data.error}>
        <WarningOutlined />
      </IconBadge>
      <NodeBody label={data.label} sub={data.sub} />
      <Handle type="target" position={Position.Left} isConnectable={isConnectable} style={handleStyle('#dc2626')} />
    </div>
  );
}

export const nodeTypes = {
  entrypoint: EntrypointNode,
  route: RouteNode,
  node: BindingNode,
  provider: ProviderNode,
  unrouted: UnroutedNode,
};
