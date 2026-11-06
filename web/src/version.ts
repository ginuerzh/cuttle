import type { Health } from './api/types';

export function shortCommit(commit?: string): string {
  return (commit ?? '').trim().slice(0, 7);
}

export function versionLabel(health: Health): string {
  const commit = shortCommit(health.commit);
  if (!commit || health.version.endsWith(commit)) {
    return health.version;
  }
  return `${health.version} · ${commit}`;
}

export function versionTitle(health: Health): string | undefined {
  const parts: string[] = [];
  if (health.commit) parts.push(`commit ${health.commit}`);
  if (health.builtAt) parts.push(`built ${health.builtAt}`);
  return parts.length ? parts.join(' · ') : undefined;
}
