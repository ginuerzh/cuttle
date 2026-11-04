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

/** APIs a provider has configured a base URL for, in canonical order. */
export function configuredAPIs(provider: Pick<Provider, 'chat' | 'responses' | 'anthropic' | 'gemini'>): ProviderApi[] {
  return apiOrder.filter((api) => (provider[api] ?? '').trim() !== '');
}

/** Display label for a model's APIs, e.g. "chat · anthropic". */
export function modelApiLabel(model: ProviderModel): string {
  return (model.apis ?? []).join(' · ');
}

/** Compact metadata line for a catalog entry; sections without data are omitted. */
export function modelMeta(model: ProviderModel): string[] {
  const out: string[] = [];
  if (model.context) out.push(`上下文 ${model.context}`);
  if (model.output) out.push(`最大输出 ${model.output}`);
  if (model.inputs?.length) out.push(`输入模态 ${model.inputs.join('/')}`);
  if (model.outputs?.length) out.push(`输出模态 ${model.outputs.join('/')}`);
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
