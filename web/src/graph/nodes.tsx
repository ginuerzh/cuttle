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
import { formatCount } from '../format';
import { translate as t } from '../i18n';
import { formatDurationMs, formatTokenRate } from '../time';
import { deadTitle, hasFailureData } from './deadTitle';

function errorRate(m?: NodeMetrics): number {
  if (!m || m.requests === 0) return 0;
  return m.errors / m.requests;
}

/**
 * Binding nodes show one of five states in a single badge slot: dead, partial
 * (dead in some routes only), unhealthy, degraded and healthy. A node with no
 * traffic keeps the neutral border and shows no badge, because it has no health
 * signal yet.
 */
export type NodeMood = 'dead' | 'partial' | 'error' | 'warn' | 'healthy' | 'idle';

export function nodeMood(m?: NodeMetrics, dead = false, partial = false): NodeMood {
  if (dead) return 'dead';
  if (partial) return 'partial';
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

function errorPercent(m: NodeMetrics): string {
  const err = errorRate(m) * 100;
  return `${err < 10 ? err.toFixed(1) : err.toFixed(0)}%`;
}

function metricsText(m: NodeMetrics, showRate: boolean): string {
  if (m.requests === 0) return t('暂无会话');
  const error = m.errors > 0 ? ` · ${errorPercent(m)}` : '';
  const rate = showRate ? formatTokenRate(m.outputTokensPerSecond) : '';
  const speed = rate ? ` · ${rate}` : '';
  return `${formatCount(m.requests)}${error} · p95 ${formatDurationMs(m.p95Ms)}${speed}`;
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

/** Dead face icon (dead-5102.svg): crossed eyes inside a ring. */
function DeadFaceIcon() {
  return (
    <svg viewBox="0 0 90 90" width="13" height="13" aria-hidden="true" focusable="false" fill="currentColor">
      <path d="M 45 90 C 20.187 90 0 69.813 0 45 C 0 20.187 20.187 0 45 0 c 24.813 0 45 20.187 45 45 C 90 69.813 69.813 90 45 90 z M 45 4 C 22.393 4 4 22.393 4 45 s 18.393 41 41 41 s 41 -18.393 41 -41 S 67.607 4 45 4 z" />
      <path d="M 63.085 63.621 h -36.17 c -1.104 0 -2 -0.896 -2 -2 s 0.896 -2 2 -2 h 36.17 c 1.104 0 2 0.896 2 2 S 64.189 63.621 63.085 63.621 z" />
      <path d="M 24.714 43.844 c -0.512 0 -1.024 -0.195 -1.414 -0.586 c -0.781 -0.781 -0.781 -2.047 0 -2.828 l 12.579 -12.579 c 0.78 -0.781 2.048 -0.781 2.828 0 c 0.781 0.781 0.781 2.047 0 2.828 L 26.128 43.258 C 25.738 43.648 25.226 43.844 24.714 43.844 z" />
      <path d="M 37.293 43.844 c -0.512 0 -1.024 -0.195 -1.414 -0.586 L 23.3 30.679 c -0.781 -0.781 -0.781 -2.047 0 -2.828 c 0.78 -0.781 2.048 -0.781 2.828 0 L 38.707 40.43 c 0.781 0.781 0.781 2.047 0 2.828 C 38.317 43.648 37.805 43.844 37.293 43.844 z" />
      <path d="M 52.707 43.844 c -0.512 0 -1.023 -0.195 -1.414 -0.586 c -0.781 -0.781 -0.781 -2.047 0 -2.828 l 12.579 -12.579 c 0.781 -0.781 2.047 -0.781 2.828 0 c 0.781 0.781 0.781 2.047 0 2.828 L 54.121 43.258 C 53.73 43.648 53.219 43.844 52.707 43.844 z" />
      <path d="M 65.286 43.844 c -0.512 0 -1.023 -0.195 -1.414 -0.586 L 51.293 30.679 c -0.781 -0.781 -0.781 -2.047 0 -2.828 c 0.781 -0.781 2.047 -0.781 2.828 0 L 66.7 40.43 c 0.781 0.781 0.781 2.047 0 2.828 C 66.31 43.648 65.798 43.844 65.286 43.844 z" />
    </svg>
  );
}

const MOOD_STYLE: Record<Exclude<NodeMood, 'idle'>, { color: string; label: string }> = {
  dead: { color: '#dc2626', label: 'dead' },
  partial: { color: '#d97706', label: '部分 dead' },
  error: { color: '#dc2626', label: '错误' },
  warn: { color: '#d97706', label: '警告' },
  healthy: { color: '#16a34a', label: '正常' },
};

/**
 * Health badge pinned inside the card's top-right corner. It is absolutely
 * positioned and pointer-transparent so it never changes the node size or
 * blocks node selection and dragging. `title` overrides the tooltip for states
 * that carry detail, such as the dead routes of a partially dead node.
 */
export function NodeMoodBadge({ mood, title }: { mood: NodeMood; title?: string }) {
  if (mood === 'idle') return null;
  const { color, label } = MOOD_STYLE[mood];
  return (
    <span
      data-status={mood}
      role="img"
      aria-label={t(label)}
      title={title ?? t(label)}
      style={{
        position: 'absolute',
        top: 5,
        right: 6,
        width: 14,
        height: 14,
        color,
        fontSize: 13,
        lineHeight: 1,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        pointerEvents: 'none',
        zIndex: 1,
      }}
    >
      {mood === 'dead' || mood === 'partial' ? (
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

/**
 * Marks a draft: an entity with no reference yet. It is amber and sits in the
 * same slot as the mood badge, which a draft never shows because it serves no
 * traffic.
 */
export function DraftBadge({ show }: { show?: boolean }) {
  if (!show) return null;
  return (
    <span
      data-status="draft"
      role="img"
      aria-label={t('未连接')}
      title={t('未连接')}
      style={{
        position: 'absolute',
        top: 3,
        right: 6,
        height: 16,
        padding: '0 4px',
        borderRadius: 4,
        background: 'rgba(217, 119, 6, 0.12)',
        color: '#d97706',
        fontSize: 10,
        fontWeight: 600,
        lineHeight: '16px',
        pointerEvents: 'none',
        zIndex: 1,
      }}
    >
      {t('未连接')}
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
          {`${formatCount(metrics.requests)}`}
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
      }}
    >
      <IconBadge color="#6366f1" pulse={data.pulse} error={data.error}>
        <GatewayOutlined />
      </IconBadge>
      <NodeBody label={data.label} sub={data.sub} />
      <NodeStats metrics={data.metrics} />
      <DraftBadge show={data.unconnected} />
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
      }}
    >
      <IconBadge color="#0ea5e9" pulse={data.pulse} error={data.error}>
        <BranchesOutlined />
      </IconBadge>
      <NodeBody label={data.label} sub={data.sub} />
      <NodeStats metrics={data.metrics} />
      <Handle type="source" position={Position.Right} isConnectable={isConnectable} style={handleStyle('#0ea5e9')} />
      <Handle type="target" position={Position.Left} isConnectable={isConnectable} style={handleStyle('#0ea5e9')} />
    </div>
  );
}

export function BindingNode({ data, selected, isConnectable }: NodeProps<GraphNode>) {
  const color = data.dead ? '#dc2626' : healthColor(data.metrics);
  const deadRoutes = data.deadRoutes ?? [];
  const partial = !data.dead && deadRoutes.length > 0;
  // A fully dead node explains itself with the same tooltip as the panel badge;
  // without failure data it keeps the plain label.
  const title = data.dead
    ? hasFailureData(data.metrics)
      ? deadTitle(data.metrics)
      : 'dead'
    : partial
      ? t('dead in {routes}', { routes: deadRoutes.join(', ') })
      : undefined;
  return (
    <div
      style={{
        ...box,
        ...selectionStyle(selected, color),
        borderStyle: data.node?.backup ? 'dashed' : 'solid',
        background: data.disabled ? 'var(--gw-node-disabled-bg)' : 'var(--gw-node-binding-bg)',
        opacity: data.disabled ? 0.55 : 1,
        minWidth: 220,
        paddingRight: 24,
      }}
      title={title}
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
      <NodeMoodBadge mood={nodeMood(data.metrics, data.dead, partial)} title={title} />
      <DraftBadge show={data.unconnected} />
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
      }}
    >
      <IconBadge color="#10b981" pulse={data.pulse} error={data.error}>
        <CloudServerOutlined />
      </IconBadge>
      <NodeBody label={data.label} sub={data.sub} />
      <NodeStats metrics={data.metrics} />
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
