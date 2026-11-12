import { useEffect, useRef } from 'react';
import { BaseEdge, EdgeLabelRenderer, getBezierPath, type EdgeProps } from '@xyflow/react';
import type { Entrypoint, EntrypointHealth } from '../api/types';
import { translate } from '../i18n';

interface FlowData extends Record<string, unknown> {
  pulse?: number;
  error?: boolean;
  duration?: number;
}

/** How one binding edge in the topology is decorated from health and config. */
export interface BindingEdgeState {
  /** The binding's probe passed, so it may serve. */
  eligible: boolean;
  /** This binding is the one that serves the next request. */
  active: boolean;
  /** No probed binding is eligible, so this binding serves as the fallback. */
  fallback: boolean;
  /**
   * The configured binding carries a probe. The health payload does not say
   * so, because the probe is inline config and no API field was added for it,
   * so this is read from `entrypoints[].routes[].probe`.
   */
  hasProbe: boolean;
  /**
   * The entrypoint has exactly one binding, so there is nothing to choose
   * between and the edge is the normal default rather than an unverified
   * alternative.
   */
  onlyBinding: boolean;
}

/** The configured bindings that carry a probe, keyed by entrypoint id + route id. */
function configuredProbes(entrypoints?: readonly Entrypoint[]): Set<string> {
  const probed = new Set<string>();
  for (const entrypoint of entrypoints ?? []) {
    for (const binding of entrypoint.routes ?? []) {
      if (binding.probe) probed.add(`${entrypoint.id}\0${binding.route}`);
    }
  }
  return probed;
}

/**
 * Keys binding health by the topology edge id, `entrypoint:<id>->route:<id>`.
 * That is the same key the graph and stats use, so a reorder never re-keys an
 * edge. A duplicate row keeps the first one, matching the first-match rule.
 * `hasProbe` comes from the console's own config, so a binding the server no
 * longer builds reads as probe-less.
 */
export function bindingEdgeHealth(
  health?: readonly EntrypointHealth[],
  entrypoints?: readonly Entrypoint[],
): Map<string, BindingEdgeState> {
  const probed = configuredProbes(entrypoints);
  const rows = new Map<string, BindingEdgeState>();
  for (const entrypoint of health ?? []) {
    const current = entrypoint.active;
    const onlyBinding = entrypoint.bindings?.length === 1;
    for (const binding of entrypoint.bindings ?? []) {
      const id = `entrypoint:${entrypoint.entrypointId}->route:${binding.routeId}`;
      if (rows.has(id)) continue;
      const isActive = current.routeId === binding.routeId;
      rows.set(id, {
        eligible: binding.eligible,
        active: isActive && !current.fallback,
        fallback: isActive && current.fallback,
        hasProbe: probed.has(`${entrypoint.entrypointId}\0${binding.routeId}`),
        onlyBinding,
      });
    }
  }
  return rows;
}

export interface BindingEdgeBadge {
  /** The failure mark of a probed binding that is not eligible. */
  mark?: '✕';
  /** Text appended after the mark; present for the binding serving a fallback. */
  text?: string;
  /** Style the edge amber, because a probe is failing. */
  invalid: boolean;
  /** Draw the edge dashed, because nothing checks this binding and it is not the entrypoint's only one. */
  dashed: boolean;
}

/**
 * The badge an edge carries. A probed binding whose probe fails is solid amber
 * with a `✕`; the fallback binding adds `回退` (`✕ 回退` when probed). A binding
 * without a probe is dashed and neutral, because nothing checks it, and carries
 * no state label unless it serves the fallback. The exception is an entrypoint
 * with a single binding: there is nothing to choose between, so that is the
 * normal case and stays solid. An eligible active binding and a binding without
 * health stay solid and unlabelled.
 */
export function bindingEdgeBadge(state?: BindingEdgeState): BindingEdgeBadge {
  if (!state) return { invalid: false, dashed: false };
  const invalid = state.hasProbe && !state.eligible;
  return {
    mark: invalid ? '✕' : undefined,
    text: state.fallback ? translate('回退') : undefined,
    invalid,
    dashed: !state.hasProbe && !state.onlyBinding,
  };
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
