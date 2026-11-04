import type { Preset, Provider, ProviderApi, ProviderModel } from '../api/types';

/** Form values a preset writes into the add-provider dialog. */
export interface PresetFormValues {
  name: string;
  type: string;
  chat: string;
  responses: string;
  anthropic: string;
  gemini: string;
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
export const PROVIDER_TYPES = ['deepseek', 'opencode-go', 'opencode-zen', 'commandcode', 'generic'] as const;

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
 * (`keysSet`, `keyPreviews`, `probe`) are display-only and must never be sent
 * back; `models` defaults to the provider's overrides so a save cannot silently
 * drop them.
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
    enabled: p.enabled !== false,
    models: overrides?.models ?? p.models ?? {},
  };
  if (p.keys && p.keys.length > 0) payload.keys = p.keys;
  return payload;
}

/** APIs a provider has configured a base URL for, in canonical order. */
export function configuredAPIs(provider: Pick<Provider, 'chat' | 'responses' | 'anthropic' | 'gemini'>): ProviderApi[] {
  return apiOrder.filter((api) => (provider[api] ?? '').trim() !== '');
}

/** Display label for a model's APIs, e.g. "chat · anthropic". */
export function modelApiLabel(model: ProviderModel): string {
  return (model.apis ?? []).join(' · ');
}

/** Render a token count compactly, e.g. 1000000 → "1M", 384000 → "384K". */
export function formatContextSize(n?: number): string | null {
  if (!n || n <= 0 || !Number.isFinite(n)) return null;
  if (n >= 1_000_000) return `${trimUnit(n / 1_000_000)}M`;
  if (n >= 1_000) {
    const thousands = Math.round(n / 1_000);
    return thousands >= 1_000 ? `${trimUnit(n / 1_000_000)}M` : `${thousands}K`;
  }
  return String(n);
}

/** Drop the trailing zeros of a one-decimal unit value, e.g. 1.0 → "1". */
function trimUnit(value: number): string {
  return String(Number(value.toFixed(1)));
}

/**
 * Reduce modality values to their MIME top-level type and dedupe them in first
 * appearance order: ["text", "image/jpeg", "image/png"] → "text/image".
 */
export function formatModalities(values?: string[]): string | null {
  const out: string[] = [];
  for (const value of values ?? []) {
    const kind = (value ?? '').trim().toLowerCase().split('/')[0];
    if (kind && !out.includes(kind)) out.push(kind);
  }
  return out.length > 0 ? out.join('/') : null;
}

/**
 * Format the USD price per million tokens. The catalog serializes an
 * unreported side as 0, so a zero side is omitted when the other side has a
 * price; two zero sides mean the model is free.
 */
export function formatPrice(price?: ProviderModel['price']): string | null {
  if (!price) return null;
  const parts: string[] = [];
  if (price.input) parts.push(`输入 $${price.input.toFixed(2)}/M`);
  if (price.output) parts.push(`输出 $${price.output.toFixed(2)}/M`);
  if (parts.length > 0) return parts.join(' · ');
  return price.input === 0 || price.output === 0 ? '免费' : null;
}

/** Display lines for a catalog entry: size limits, modalities and price. */
export function modelMeta(model: ProviderModel): string[] {
  const out: string[] = [];

  const sizes: string[] = [];
  const context = formatContextSize(model.context);
  if (context) sizes.push(`上下文 ${context}`);
  const input = formatContextSize(model.input);
  if (input) sizes.push(`输入 ${input}`);
  const output = formatContextSize(model.output);
  if (output) sizes.push(`输出 ${output}`);
  if (sizes.length > 0) out.push(sizes.join(' · '));

  const modalities: string[] = [];
  const inputs = formatModalities(model.inputs);
  if (inputs) modalities.push(`输入模态 ${inputs}`);
  const outputs = formatModalities(model.outputs);
  if (outputs) modalities.push(`输出模态 ${outputs}`);
  if (modalities.length > 0) out.push(modalities.join(' · '));

  const price = formatPrice(model.price);
  if (price) out.push(`价格 ${price}`);

  return out;
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
 * their catalog records source APIs, but routing still follows the canonical
 * configured-base order in v1.
 */
export function genericCanonicalNote(providerType: string, configured: ProviderApi[]): string | null {
  if (providerType.trim().toLowerCase() !== 'generic' || configured.length < 2) return null;
  return `generic 提供商按固定顺序 ${apiOrder.join(' → ')} 选择目标 API；模型目录中的 APIs 不参与路由。`;
}
