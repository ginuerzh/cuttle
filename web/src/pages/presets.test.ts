import { describe, expect, it } from 'vitest';
import type { Preset, Provider, ProviderModel } from '../api/types';
import {
  configuredAPIs,
  derivedTargetAPI,
  genericCanonicalNote,
  modelApiLabel,
  modelMeta,
  modelOption,
  presetFormValues,
  unknownModels,
} from './presets';

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

describe('modelMeta', () => {
  it('reports context, output and modalities when present', () => {
    expect(
      modelMeta({ id: 'm1', context: 128000, output: 8192, inputs: ['text', 'image'], outputs: ['text'] }),
    ).toEqual(['上下文 128000', '最大输出 8192', '输入模态 text/image', '输出模态 text']);
  });

  it('is empty when the catalog has no metadata', () => {
    expect(modelMeta({ id: 'm1' })).toEqual([]);
  });
});
