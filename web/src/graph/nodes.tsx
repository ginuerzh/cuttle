import { Handle, Position, type NodeProps } from '@xyflow/react';
import {
  ApiOutlined,
  BranchesOutlined,
  CloudServerOutlined,
  TagOutlined,
  WarningOutlined,
} from '@ant-design/icons';
import type { NodeMetrics } from '../api/types';
import type { GraphNode } from './build';

function errorRate(m?: NodeMetrics): number {
  if (!m || m.requests === 0) return 0;
  return m.errors / m.requests;
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

function metricsLine(m?: NodeMetrics): string {
  if (!m) return '';
  if (m.requests === 0) return '暂无请求';
  const err = errorRate(m) * 100;
  return `${fmt(m.requests)} req · ${err < 10 ? err.toFixed(1) : err.toFixed(0)}% err · p95 ${fmt(m.p95Ms)}ms`;
}

const box: React.CSSProperties = {
  position: 'relative',
  display: 'flex',
  alignItems: 'flex-start',
  gap: 10,
  borderRadius: 10,
  border: '1.5px solid',
  padding: '10px 12px',
  background: 'var(--gw-surface)',
  fontSize: 12,
  color: 'var(--gw-text)',
  boxShadow: 'var(--gw-node-shadow)',
};

function IconBadge({ color, children }: { color: string; children: React.ReactNode }) {
  return (
    <div
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

function Body({ label, sub, metrics }: { label: string; sub?: string; metrics?: NodeMetrics }) {
  return (
    <div style={{ minWidth: 0 }}>
      <div style={{ fontWeight: 600, fontSize: 13, whiteSpace: 'nowrap' }}>{label}</div>
      {sub ? <div style={{ color: 'var(--gw-text-muted)', fontSize: 11, marginTop: 2 }}>{sub}</div> : null}
      {metrics ? (
        <div style={{ marginTop: 6, fontSize: 11, color: 'var(--gw-text-metrics)' }}>{metricsLine(metrics)}</div>
      ) : null}
    </div>
  );
}

export function AliasNode({ data }: NodeProps<GraphNode>) {
  return (
    <div style={{ ...box, borderColor: '#6366f1', background: 'var(--gw-node-alias-bg)', minWidth: 220 }}>
      <IconBadge color="#6366f1">
        <TagOutlined />
      </IconBadge>
      <Body label={data.label} sub={data.sub} />
      <Handle type="source" position={Position.Right} style={{ background: '#6366f1' }} />
    </div>
  );
}

export function RouteNode({ data, selected }: NodeProps<GraphNode>) {
  const color = healthColor(data.metrics);
  return (
    <div
      style={{
        ...box,
        borderColor: selected ? '#0ea5e9' : color,
        background: 'var(--gw-node-route-bg)',
        minWidth: 250,
      }}
    >
      <IconBadge color="#0ea5e9">
        <BranchesOutlined />
      </IconBadge>
      <Body label={data.label} sub={data.sub} metrics={data.metrics} />
      <Handle type="source" position={Position.Right} style={{ background: '#0ea5e9' }} />
      <Handle type="target" position={Position.Left} style={{ background: '#0ea5e9' }} />
    </div>
  );
}

export function BindingNode({ data, selected }: NodeProps<GraphNode>) {
  const color = healthColor(data.metrics);
  return (
    <div
      style={{
        ...box,
        borderStyle: data.node?.backup ? 'dashed' : 'solid',
        borderColor: selected ? '#0ea5e9' : color,
        background: data.disabled ? 'var(--gw-node-disabled-bg)' : 'var(--gw-node-binding-bg)',
        opacity: data.disabled ? 0.55 : 1,
        minWidth: 220,
      }}
    >
      <IconBadge color="#0891b2">
        <ApiOutlined />
      </IconBadge>
      <Body label={data.label} sub={data.sub} metrics={data.metrics} />
      <Handle type="target" position={Position.Left} style={{ background: '#0891b2' }} />
      <Handle type="source" position={Position.Right} style={{ background: '#0891b2' }} />
    </div>
  );
}

export function ProviderNode({ data, selected }: NodeProps<GraphNode>) {
  const color = healthColor(data.metrics);
  return (
    <div
      style={{
        ...box,
        borderColor: selected ? '#0ea5e9' : color,
        background: data.disabled ? 'var(--gw-node-disabled-bg)' : 'var(--gw-node-provider-bg)',
        opacity: data.disabled ? 0.55 : 1,
        minWidth: 230,
      }}
    >
      <IconBadge color="#10b981">
        <CloudServerOutlined />
      </IconBadge>
      <Body label={data.label} sub={data.sub} metrics={data.metrics} />
      <Handle type="target" position={Position.Left} style={{ background: '#10b981' }} />
    </div>
  );
}

export function UnroutedNode({ data }: NodeProps<GraphNode>) {
  return (
    <div style={{ ...box, borderColor: '#dc2626', background: 'var(--gw-node-unrouted-bg)', minWidth: 190 }}>
      <IconBadge color="#dc2626">
        <WarningOutlined />
      </IconBadge>
      <Body label={data.label} sub={data.sub} />
      <Handle type="target" position={Position.Left} style={{ background: '#dc2626' }} />
    </div>
  );
}

export const nodeTypes = {
  alias: AliasNode,
  route: RouteNode,
  node: BindingNode,
  provider: ProviderNode,
  unrouted: UnroutedNode,
};
