import { describe, expect, it } from 'vitest';
import type { Preset, Provider, ProviderModel } from '../api/types';
import { setActiveLang } from '../i18n';
import {
  configuredAPIs,
  derivedTargetAPI,
  disabledTargets,
  enabledModels,
  formatContextSize,
  formatModalities,
  formatPrice,
  genericCanonicalNote,
  isBaseURLLocked,
  modelApiLabel,
  modelDirectionRows,
  modelOption,
  normalizedModalities,
  presetFormValues,
  providerPayload,
  toggleModelOverride,
  unknownModels,
} from './presets';

setActiveLang('zh-CN');

const opencodeGo: Preset = {
  name: 'opencode-go',
  type: 'opencode-go',
  chat: 'https://opencode.ai/zen/go/v1',
  responses: 'https://opencode.ai/zen/go/v1',
  anthropic: 'https://opencode.ai/zen/go',
};

const opencodeZen: Preset = {
  name: 'opencode-zen',
  type: 'opencode-zen',
  chat: 'https://opencode.ai/zen/v1',
  responses: 'https://opencode.ai/zen/v1',
  anthropic: 'https://opencode.ai/zen',
};

describe('presetFormValues', () => {
  it('fills the name, type and every API base URL', () => {
    expect(presetFormValues(opencodeGo)).toEqual({
      name: 'opencode-go',
      type: 'opencode-go',
      chat: 'https://opencode.ai/zen/go/v1',
      responses: 'https://opencode.ai/zen/go/v1',
      anthropic: 'https://opencode.ai/zen/go',
      gemini: '',
    });
  });

  it('fills different base URLs for opencode-go and opencode-zen', () => {
    const go = presetFormValues(opencodeGo);
    const zen = presetFormValues(opencodeZen);
    expect(go.chat).not.toBe(zen.chat);
    expect(go.anthropic).not.toBe(zen.anthropic);
    expect(go.name).not.toBe(zen.name);
  });

  it('selecting a preset does not require a protocol', () => {
    const values = presetFormValues(opencodeZen);
    expect(values).not.toHaveProperty('protocol');
    expect(values).not.toHaveProperty('pathMap');
  });
});

describe('modelApiLabel', () => {
  it('joins every API the model serves', () => {
    expect(modelApiLabel({ id: 'deepseek-v4-flash', apis: ['chat', 'anthropic'] })).toBe('chat · anthropic');
  });

  it('is empty when the catalog does not know the APIs', () => {
    expect(modelApiLabel({ id: 'mystery-model' })).toBe('');
  });
});

const provider: Provider = {
  name: 'p',
  type: 'generic',
  chat: ' https://a.example/v1 ',
  responses: '',
  anthropic: 'https://a.example/anthropic',
  gemini: '  ',
  keysSet: 0,
  keyPreviews: [],
};

describe('configuredAPIs', () => {
  it('lists the APIs with a non-blank base URL in canonical order', () => {
    expect(configuredAPIs(provider)).toEqual(['chat', 'anthropic']);
  });

  it('is empty when the provider has no base URL', () => {
    expect(configuredAPIs({})).toEqual([]);
  });
});

describe('modelOption', () => {
  it('labels a model with its name, id and APIs', () => {
    expect(modelOption({ id: 'm1', name: 'Model One', apis: ['chat', 'responses'] })).toEqual({
      value: 'm1',
      label: 'Model One (m1) · chat · responses',
    });
  });

  it('does not repeat the id when the name matches it', () => {
    expect(modelOption({ id: 'm1', name: 'm1', apis: ['chat'] })).toEqual({
      value: 'm1',
      label: 'm1 · chat',
    });
  });

  it('omits the API suffix when the catalog does not know the APIs', () => {
    expect(modelOption({ id: 'm1' })).toEqual({ value: 'm1', label: 'm1' });
  });
});

describe('unknownModels', () => {
  const models: ProviderModel[] = [
    { id: 'm1', apis: ['chat'] },
    { id: 'no-apis' },
    { id: 'empty-apis', apis: [] },
    { id: 'anthropic-only', apis: ['anthropic'] },
  ];

  it('flags a target model that is not in the catalog', () => {
    expect(unknownModels([{ key: 'fast', value: 'mystery-model' }], models)).toEqual(['mystery-model']);
  });

  it('flags a catalog model whose APIs are missing or empty', () => {
    expect(
      unknownModels(
        [
          { key: 'fast', value: 'no-apis' },
          { key: 'strong', value: 'empty-apis' },
        ],
        models,
      ),
    ).toEqual(['no-apis', 'empty-apis']);
  });

  it('ignores known models with APIs, blank values, * and duplicates', () => {
    expect(
      unknownModels(
        [
          { key: 'fast', value: 'm1' },
          { key: 'strong', value: 'anthropic-only' },
          { key: 'blank', value: '  ' },
          { key: '*', value: '*' },
          { key: 'dup', value: 'no-apis' },
          { key: 'dup2', value: 'no-apis' },
        ],
        models,
      ),
    ).toEqual(['no-apis']);
  });

  it('does not warn before the catalog has loaded', () => {
    expect(unknownModels([{ key: 'fast', value: 'mystery-model' }], [])).toEqual([]);
  });
});

describe('derivedTargetAPI', () => {
  it('picks the first model API that has a configured base URL', () => {
    expect(derivedTargetAPI('deepseek', ['chat', 'anthropic'], ['anthropic', 'chat'])).toBe('anthropic');
  });

  it('skips a model API without a configured base URL', () => {
    expect(derivedTargetAPI('deepseek', ['anthropic'], ['chat', 'anthropic'])).toBe('anthropic');
  });

  it('falls back to the canonical provider order when the model has no APIs', () => {
    expect(derivedTargetAPI('deepseek', ['gemini', 'chat'], undefined)).toBe('chat');
    expect(derivedTargetAPI('deepseek', ['gemini', 'chat'], [])).toBe('chat');
  });

  it('ignores catalog APIs for generic providers and uses canonical order', () => {
    expect(derivedTargetAPI('generic', ['gemini', 'chat'], ['gemini'])).toBe('chat');
    expect(derivedTargetAPI('Generic', ['responses', 'anthropic'], ['anthropic'])).toBe('responses');
    expect(derivedTargetAPI('generic', ['anthropic'], ['anthropic', 'chat'])).toBe('anthropic');
  });

  it('returns unknown when no configured API can serve the model', () => {
    expect(derivedTargetAPI('deepseek', ['gemini'], ['chat'])).toBe('unknown');
    expect(derivedTargetAPI('deepseek', [], ['chat'])).toBe('unknown');
    expect(derivedTargetAPI('deepseek', [], undefined)).toBe('unknown');
    expect(derivedTargetAPI('generic', [], ['chat'])).toBe('unknown');
  });
});

describe('genericCanonicalNote', () => {
  it('explains the canonical routing order for generic providers with multiple APIs', () => {
    expect(genericCanonicalNote('generic', ['chat', 'anthropic'])).toBe(
      'generic 提供商按固定顺序 chat → responses → anthropic → gemini 选择目标 API；模型目录中的 APIs 不参与路由。',
    );
  });

  it('is absent for non-generic providers or a single configured API', () => {
    expect(genericCanonicalNote('deepseek', ['chat', 'anthropic'])).toBeNull();
    expect(genericCanonicalNote('generic', ['chat'])).toBeNull();
    expect(genericCanonicalNote('generic', [])).toBeNull();
  });
});

describe('formatContextSize', () => {
  it('formats K/M values and leaves small values alone', () => {
    expect(formatContextSize(1_000_000)).toBe('1M');
    expect(formatContextSize(384_000)).toBe('384K');
    expect(formatContextSize(999)).toBe('999');
    expect(formatContextSize(0)).toBeNull();
    expect(formatContextSize(undefined)).toBeNull();
  });

  it('rounds to whole units for common context sizes', () => {
    expect(formatContextSize(128_000)).toBe('128K');
    expect(formatContextSize(8_192)).toBe('8K');
    expect(formatContextSize(999_500)).toBe('1M');
  });
});

describe('formatModalities', () => {
  it('normalizes MIME values and dedupes', () => {
    expect(formatModalities(['text', 'image/jpeg', 'image/png'])).toBe('text/image');
  });

  it('trims, lowercases and strips MIME subtypes', () => {
    expect(formatModalities([' Text ', 'IMAGE/JPEG', 'image/png'])).toBe('text/image');
  });

  it('drops unknown top-level types but keeps known ones', () => {
    expect(formatModalities(['text', 'application/pdf', 'image / jpeg'])).toBe('text/file/image');
  });

  it('returns null for empty input', () => {
    expect(formatModalities([])).toBeNull();
    expect(formatModalities(undefined)).toBeNull();
  });
});

describe('normalizedModalities', () => {
  it('returns known types in first-seen order', () => {
    expect(normalizedModalities(['image/png', 'text', 'IMAGE/JPEG', 'application/pdf'])).toEqual(['image', 'text', 'file']);
  });

  it('maps application MIME types to file', () => {
    expect(normalizedModalities(['application/pdf'])).toEqual(['file']);
  });

  it('returns an empty array when nothing is known', () => {
    expect(normalizedModalities(['x-unknown/thing'])).toEqual([]);
    expect(normalizedModalities(undefined)).toEqual([]);
  });
});

describe('formatPrice', () => {
  it('formats input/output USD per million tokens', () => {
    expect(formatPrice({ input: 0.15, output: 0.6 })).toBe('输入 $0.15/M · 输出 $0.60/M');
  });

  it('shows free when both sides are zero', () => {
    expect(formatPrice({ input: 0, output: 0 })).toBe('免费');
  });

  it('omits absent sides', () => {
    expect(formatPrice({ input: 0.15 })).toBe('输入 $0.15/M');
    expect(formatPrice(undefined)).toBeNull();
    expect(formatPrice({})).toBeNull();
  });

  it('omits a zero side when the other side is priced', () => {
    expect(formatPrice({ input: 0.15, output: 0 })).toBe('输入 $0.15/M');
    expect(formatPrice({ input: 0, output: 0.6 })).toBe('输出 $0.60/M');
  });

  it('treats a lone zero side as unreported rather than free', () => {
    expect(formatPrice({ input: 0 })).toBeNull();
    expect(formatPrice({ output: 0 })).toBeNull();
  });
});

describe('modelDirectionRows', () => {
  it('groups output fields first and input fields second', () => {
    expect(
      modelDirectionRows({
        id: 'm1',
        context: 1_000_000,
        input: 1_000_000,
        output: 384_000,
        inputs: ['text', 'image'],
        outputs: ['text'],
        price: { input: 0.15, output: 0.6 },
      }),
    ).toEqual({
      output: '出 384K · $0.60/M',
      input: '入 1M · $0.15/M',
    });
  });

  it('shows the context window when it differs from the input limit', () => {
    expect(
      modelDirectionRows({
        id: 'm1',
        context: 200_000,
        input: 160_000,
        output: 32_000,
        price: { input: 0.15, output: 0.6 },
      }),
    ).toEqual({
      output: '出 32K · $0.60/M',
      input: '入 160K · 上下文 200K · $0.15/M',
    });
  });

  it('falls back to the context window when limit.input is absent', () => {
    expect(modelDirectionRows({ id: 'm1', context: 1_000_000, output: 384_000 })).toEqual({
      output: '出 384K',
      input: '入 1M',
    });
  });

  it('marks both sides free when both prices are zero', () => {
    expect(
      modelDirectionRows({
        id: 'm1',
        context: 32_000,
        input: 32_000,
        output: 8_000,
        price: { input: 0, output: 0 },
      }),
    ).toEqual({
      output: '出 8K · 免费',
      input: '入 32K · 免费',
    });
  });

  it('is empty when the catalog has no size or price data', () => {
    expect(modelDirectionRows({ id: 'm1' })).toEqual({ output: null, input: null });
  });
});

describe('isBaseURLLocked', () => {
  it('locks every provider type except generic', () => {
    expect(isBaseURLLocked('deepseek')).toBe(true);
    expect(isBaseURLLocked('opencode-go')).toBe(true);
    expect(isBaseURLLocked('generic')).toBe(false);
    expect(isBaseURLLocked(' Generic ')).toBe(false);
  });
});

describe('enabledModels', () => {
  it('drops disabled models and keeps the rest', () => {
    expect(
      enabledModels([
        { id: 'm1' },
        { id: 'm2', disabled: false },
        { id: 'm3', disabled: true },
      ]),
    ).toEqual([{ id: 'm1' }, { id: 'm2', disabled: false }]);
  });

  it('keeps the catalog order', () => {
    expect(enabledModels([{ id: 'b' }, { id: 'a' }]).map((m) => m.id)).toEqual(['b', 'a']);
  });
});

describe('disabledTargets', () => {
  const models: ProviderModel[] = [
    { id: 'm1', apis: ['chat'] },
    { id: 'off', apis: ['chat'], disabled: true },
    { id: 'off2', apis: ['anthropic'], disabled: true },
  ];

  it('returns catalog values whose entry is disabled', () => {
    expect(
      disabledTargets(
        [
          { key: 'fast', value: 'off' },
          { key: 'strong', value: 'off2' },
        ],
        models,
      ),
    ).toEqual(['off', 'off2']);
  });

  it('ignores *, blanks, enabled models, unknown models and duplicates', () => {
    expect(
      disabledTargets(
        [
          { key: '*', value: '*' },
          { key: 'blank', value: '  ' },
          { key: 'enabled', value: 'm1' },
          { key: 'unknown', value: 'mystery-model' },
          { key: 'dup', value: 'off' },
          { key: 'dup2', value: 'off' },
        ],
        models,
      ),
    ).toEqual(['off']);
  });

  it('does not warn before the catalog has loaded or without rows', () => {
    expect(disabledTargets([{ key: 'fast', value: 'off' }], [])).toEqual([]);
    expect(disabledTargets(undefined, models)).toEqual([]);
  });
});

describe('toggleModelOverride', () => {
  it('adds a disabled override', () => {
    expect(toggleModelOverride(undefined, 'm1', true)).toEqual({ m1: { disabled: true } });
  });

  it('removes the key when re-enabled and preserves other overrides', () => {
    expect(toggleModelOverride({ m1: { disabled: true }, m2: { disabled: true } }, 'm1', false)).toEqual({
      m2: { disabled: true },
    });
  });

  it('does not mutate the input map', () => {
    const before = { m1: { disabled: true } };
    const next = toggleModelOverride(before, 'm1', false);
    expect(before).toEqual({ m1: { disabled: true } });
    expect(next).toEqual({});
  });
});

describe('providerPayload', () => {
  const full: Provider = {
    name: 'p',
    type: 'deepseek',
    chat: 'https://chat.example/v1',
    responses: '',
    anthropic: 'https://anthropic.example',
    gemini: '',
    keysSet: 2,
    keyPreviews: ['sk-...abcd'],
    auth: { header: 'Authorization' },
    health: { api: 'chat', path: '/models' },
    tls: { secure: false },
    enabled: false,
    models: { m1: { disabled: true } },
    probe: { ok: true, latencyMs: 12 },
  };

  it('keeps server fields and drops view-only fields', () => {
    const payload = providerPayload(full);
    expect(payload).toEqual({
      name: 'p',
      type: 'deepseek',
      chat: 'https://chat.example/v1',
      responses: '',
      anthropic: 'https://anthropic.example',
      gemini: '',
      auth: { header: 'Authorization' },
      health: { api: 'chat', path: '/models' },
      tls: { secure: false },
      enabled: false,
      models: { m1: { disabled: true } },
    });
    expect(payload).not.toHaveProperty('keysSet');
    expect(payload).not.toHaveProperty('keyPreviews');
    expect(payload).not.toHaveProperty('probe');
  });

  it('uses models from the override argument', () => {
    expect(providerPayload(full, { models: { m2: { disabled: true } } }).models).toEqual({
      m2: { disabled: true },
    });
  });

  it('includes empty objects for optional server fields and defaults enabled to true', () => {
    expect(providerPayload({ name: 'bare', type: 'generic', keysSet: 0, keyPreviews: [] })).toMatchObject({
      auth: {},
      health: {},
      tls: {},
      models: {},
      enabled: true,
    });
  });

  it('includes keys only when the provider carries them', () => {
    expect(providerPayload({ ...full, keys: ['sk-1'] })).toMatchObject({ keys: ['sk-1'] });
    expect(providerPayload(full)).not.toHaveProperty('keys');
  });
});
