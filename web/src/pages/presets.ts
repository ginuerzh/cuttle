import type { NodeAPI, Preset, Provider, ProviderApi, ProviderKeyView, ProviderModel } from '../api/types';
import { formatCount } from '../format';
import { translate as t } from '../i18n';

/** Form values a preset writes into the add-provider dialog. */
export interface PresetFormValues {
  name: string;
  type: string;
  chat: string;
  responses: string;
  anthropic: string;
  gemini: string;
}

/** One editable provider-key row. Existing rows keep their id and masked preview. */
export interface ProviderKeyRow {
  /** Stored key id; absent for a row that is being added. */
  id?: string;
  name: string;
  preview?: string;
  value: string;
  enabled: boolean;
  originalName: string;
  originalEnabled: boolean;
  existing: boolean;
}

/** One key operation sent to the admin API; no secret is returned. */
export interface ProviderKeyUpdate {
  /** Stored key id; absent when the row appends a new key. */
  id?: string;
  name?: string;
  key?: string;
  enabled?: boolean;
  delete?: boolean;
}

export function providerKeyRows(keys: ProviderKeyView[]): ProviderKeyRow[] {
  return keys.map((k) => ({
    id: k.id,
    name: k.name,
    preview: k.keyPreview,
    value: '',
    enabled: k.enabled,
    originalName: k.name,
    originalEnabled: k.enabled,
    existing: true,
  }));
}

/**
 * Turn the editor rows into key updates. An existing key is addressed by its
 * id, so a rename, a toggle and a delete all keep the same identity; only a
 * new row is addressed by name, and the admin API mints its id.
 */
export function providerKeyUpdates(rows: ProviderKeyRow[], deleted: string[]): ProviderKeyUpdate[] {
  const updates: ProviderKeyUpdate[] = deleted.map((id) => ({ id, delete: true }));
  for (const row of rows) {
    if (!row.existing || !row.id || deleted.includes(row.id)) continue;
    const name = row.name.trim();
    if (!name) continue;
    if (name !== row.originalName || row.enabled !== row.originalEnabled) {
      updates.push({ id: row.id, name, enabled: row.enabled });
    }
  }
  for (const row of rows) {
    if (row.existing) continue;
    const name = row.name.trim();
    const key = row.value.trim();
    if (name && key) updates.push({ name, key, enabled: row.enabled });
  }
  return updates;
}

/** Turn a preset into form values; blank base URLs stay blank, never undefined. */
export function presetFormValues(preset: Preset): PresetFormValues {
  return {
    name: preset.name,
    type: preset.type,
    chat: preset.chat ?? '',
    responses: preset.responses ?? '',
    anthropic: preset.anthropic ?? '',
    gemini: preset.gemini ?? '',
  };
}

const apiOrder: ProviderApi[] = ['chat', 'responses', 'anthropic', 'gemini'];

/** Provider adapter names accepted by the backend configuration. */
export const PROVIDER_TYPES = ['deepseek', 'opencode-go', 'opencode-zen', 'commandcode', 'openrouter', 'generic'] as const;

/**
 * Provider identity is fixed at creation time. Only `generic` providers may
 * change their API base URLs afterwards; every preset type keeps its URLs.
 */
export function isBaseURLLocked(providerType: string): boolean {
  return providerType.trim().toLowerCase() !== 'generic';
}

/** Catalog models that remain selectable for a node mapping. */
export function enabledModels(models: ProviderModel[]): ProviderModel[] {
  return models.filter((m) => !m.disabled);
}

/**
 * Build the server-side provider payload from a provider view. Derived fields
 * (`keys`, `probe`) are display-only and must never be sent back; `models`
 * defaults to the provider's overrides so a save cannot silently drop them.
 */
export function providerPayload(
  p: Provider,
  overrides?: Partial<Pick<Provider, 'models'>>,
): Record<string, unknown> {
  const payload: Record<string, unknown> = {
    name: p.name,
    type: p.type,
    chat: p.chat ?? '',
    responses: p.responses ?? '',
    anthropic: p.anthropic ?? '',
    gemini: p.gemini ?? '',
    auth: p.auth ?? {},
    health: p.health ?? {},
    tls: p.tls ?? {},
    userAgent: p.userAgent ?? '',
    headers: p.headers ?? {},
    enabled: p.enabled !== false,
    models: overrides?.models ?? p.models ?? {},
  };
  return payload;
}

/** Render upstream headers as an editable JSON object. */
export function headersText(headers?: Record<string, string>): string {
  if (!headers || Object.keys(headers).length === 0) return '';
  return JSON.stringify(headers, null, 2);
}

/** Parse the provider form's header JSON and reject non-string values. */
export function parseHeadersText(text: string): Record<string, string> {
  const raw = text.trim();
  if (!raw) return {};
  const parsed: unknown = JSON.parse(raw);
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw new Error(t('headers 必须是 JSON 对象'));
  }
  const out: Record<string, string> = {};
  for (const [key, value] of Object.entries(parsed as Record<string, unknown>)) {
    if (typeof value !== 'string') {
      throw new Error(t('header {name} 的值必须是字符串', { name: key }));
    }
    out[key] = value;
  }
  return out;
}

/** APIs a provider has configured a base URL for, in canonical order. */
export function configuredAPIs(provider: Pick<Provider, 'chat' | 'responses' | 'anthropic' | 'gemini'>): ProviderApi[] {
  return apiOrder.filter((api) => (provider[api] ?? '').trim() !== '');
}

/** API choices for a node: auto first, then the provider's configured APIs. */
export function nodeAPIOptions(
  provider?: Pick<Provider, 'chat' | 'responses' | 'anthropic' | 'gemini'>,
): NodeAPI[] {
  return ['auto', ...(provider ? configuredAPIs(provider) : [])];
}

/** Options for the node key pin select; the empty value means every enabled key. */
export function nodeKeyOptions(keys: ProviderKeyView[]): { value: string; label: string }[] {
  return [
    { value: '', label: t('全部 key（轮询）') },
    ...keys.map((k) => ({ value: k.id, label: k.enabled ? k.name : `${k.name}${t('（已禁用）')}` })),
  ];
}

/** Display label for a model's APIs, e.g. "chat · anthropic". */
export function modelApiLabel(model: ProviderModel): string {
  return (model.apis ?? []).join(' · ');
}

/** Render a token limit compactly with the shared K/M/B unit scale. */
export function formatContextSize(n?: number): string | null {
  if (!n || n <= 0 || !Number.isFinite(n)) return null;
  return formatCount(n);
}

/** Top-level modality types the UI knows how to display. */
const MODALITY_TYPES = new Set(['text', 'image', 'audio', 'video', 'file']);

/**
 * Reduce modality values to their MIME top-level type, map application MIME
 * types to "file", keep only the known types, and dedupe them in first
 * appearance order:
 * ["text", "application/pdf", "image/jpeg"] → ["text", "file", "image"].
 */
export function normalizedModalities(values?: string[]): string[] {
  const out: string[] = [];
  for (const value of values ?? []) {
    let kind = (value ?? '').trim().toLowerCase().split('/')[0].trim();
    if (kind === 'application') kind = 'file';
    if (MODALITY_TYPES.has(kind) && !out.includes(kind)) out.push(kind);
  }
  return out;
}

/** Display label for modalities, e.g. "text/image". */
export function formatModalities(values?: string[]): string | null {
  const out = normalizedModalities(values);
  return out.length > 0 ? out.join('/') : null;
}

/**
 * Format the USD price per million tokens. A lone zero side is treated as
 * unreported because it cannot be distinguished from a missing side; two zero
 * sides mean the model is free. A zero side next to a priced side is omitted.
 */
export function formatPrice(price?: ProviderModel['price']): string | null {
  if (!price) return null;
  if (
    (typeof price.input === 'number' && price.input < 0) ||
    (typeof price.output === 'number' && price.output < 0)
  ) {
    return null;
  }
  const hasInput = typeof price.input === 'number';
  const hasOutput = typeof price.output === 'number';
  if (!hasInput && !hasOutput) return null;
  if (hasInput && hasOutput && price.input === 0 && price.output === 0) return t('免费');
  const parts: string[] = [];
  if (typeof price.input === 'number' && price.input !== 0) {
    parts.push(t('输入 ${price}/M', { price: price.input.toFixed(2) }));
  }
  if (typeof price.output === 'number' && price.output !== 0) {
    parts.push(t('输出 ${price}/M', { price: price.output.toFixed(2) }));
  }
  return parts.length > 0 ? parts.join(' · ') : null;
}

/** Per-direction card rows: output first, then input, each combining size and price. */
export interface ModelDirectionRows {
  output: string | null;
  input: string | null;
}

/** One side of the USD price, omitting an unreported zero. */
function priceSide(value: number | undefined, free: boolean): string | null {
  if (typeof value !== 'number' || value < 0) return null;
  if (free) return t('免费');
  if (value === 0) return null;
  return `$${value.toFixed(2)}/M`;
}

/**
 * Build the two compact model-card rows. Input-related fields share the first
 * line and output-related fields the next. `limit.input` falls back to the
 * context window; when both differ, the context window is shown alongside the
 * input.
 */
export function modelDirectionRows(model: ProviderModel): ModelDirectionRows {
  const context = formatContextSize(model.context);
  const inputSize = formatContextSize(model.input);
  const outputSize = formatContextSize(model.output);
  const price = model.price;
  const free = !!price && price.input === 0 && price.output === 0;

  const inputParts: string[] = [];
  if (inputSize) inputParts.push(t('入 {size}', { size: inputSize }));
  else if (context) inputParts.push(t('入 {size}', { size: context }));
  if (context && inputSize && inputSize !== context) inputParts.push(t('上下文 {size}', { size: context }));
  const inputPrice = priceSide(price?.input, free);
  if (inputPrice) inputParts.push(inputPrice);

  const outputParts: string[] = [];
  if (outputSize) outputParts.push(t('出 {size}', { size: outputSize }));
  const outputPrice = priceSide(price?.output, free);
  if (outputPrice) outputParts.push(outputPrice);

  return {
    output: outputParts.length > 0 ? outputParts.join(' · ') : null,
    input: inputParts.length > 0 ? inputParts.join(' · ') : null,
  };
}

/** AutoComplete option for one model, showing its name and supported APIs. */
export function modelOption(model: ProviderModel): { value: string; label: string } {
  const apis = modelApiLabel(model);
  const name = model.name && model.name !== model.id ? `${model.name} (${model.id})` : model.id;
  return { value: model.id, label: apis ? `${name} · ${apis}` : name };
}

/**
 * Target model values that need a warning: not in the catalog, or in it with
 * no known APIs (the gateway then falls back to the provider's default API).
 * `*` stays a manual special value; an unloaded catalog produces no warnings.
 */
export function unknownModels(rows: { key?: string; value?: string }[] | undefined, models: ProviderModel[]): string[] {
  if (models.length === 0) return [];
  const byID = new Map(models.map((m) => [m.id, m]));
  const out: string[] = [];
  for (const row of rows ?? []) {
    const value = (row?.value ?? '').trim();
    if (!value || value === '*' || out.includes(value)) continue;
    const model = byID.get(value);
    if (model && (model.apis?.length ?? 0) > 0) continue;
    out.push(value);
  }
  return out;
}

/**
 * Target model values whose catalog entry is locally disabled. Such mappings
 * remain saveable, but the gateway skips the node, so they get a dedicated
 * warning instead of the unknown-model one. `*` and blanks are ignored, and an
 * unloaded catalog produces no warnings.
 */
export function disabledTargets(rows: { key?: string; value?: string }[] | undefined, models: ProviderModel[]): string[] {
  if (models.length === 0) return [];
  const disabled = new Set(models.filter((m) => m.disabled === true).map((m) => m.id));
  const out: string[] = [];
  for (const row of rows ?? []) {
    const value = (row?.value ?? '').trim();
    if (!value || value === '*' || out.includes(value)) continue;
    if (disabled.has(value)) out.push(value);
  }
  return out;
}

/**
 * Update one disabled override without mutating the provider's map. Enabling a
 * model removes its key entirely so `disabled: false` never reaches the
 * backend; other model overrides stay intact.
 */
export function toggleModelOverride(
  models: Provider['models'],
  id: string,
  disabled: boolean,
): NonNullable<Provider['models']> {
  const next = { ...(models ?? {}) };
  if (disabled) next[id] = { disabled: true };
  else delete next[id];
  return next;
}

/**
 * API the gateway will use for a target model: the first API the model
 * advertises that also has a configured base URL; for models without a known
 * API classification, the first configured API in canonical order.
 *
 * `generic` providers are special: the v1 gateway ignores catalog APIs for
 * them and always uses the canonical configured-base order, so the hint must
 * mirror that instead of promising catalog-aware routing.
 */
export function derivedTargetAPI(
  providerType: string,
  configured: ProviderApi[],
  modelAPIs?: ProviderApi[],
): ProviderApi | 'unknown' {
  if (providerType.trim().toLowerCase() !== 'generic' && modelAPIs && modelAPIs.length > 0) {
    return modelAPIs.find((api) => configured.includes(api)) ?? 'unknown';
  }
  return apiOrder.find((api) => configured.includes(api)) ?? 'unknown';
}

/**
 * Explanation shown for generic providers with more than one configured API:
 * their model catalog has no static classification, so auto matches the
 * inbound protocol to a configured base URL and falls back to canonical order.
 */
export function genericCanonicalNote(providerType: string, configured: ProviderApi[]): string | null {
  if (providerType.trim().toLowerCase() !== 'generic' || configured.length < 2) return null;
  return t('generic 提供商没有静态模型 API 分类；auto 先按入站协议匹配已配置的 base URL，匹配不到时按 {order} 回退。', { order: apiOrder.join(' → ') });
}
