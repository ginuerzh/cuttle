import type { ReactNode } from 'react';
import { Tag, Tooltip, Typography } from 'antd';
import type { ProviderModel } from '../api/types';
import { translate as t } from '../i18n';
import { modelApiLabel, modelDirectionRows, normalizedModalities } from '../pages/presets';
import { MobileCard } from './MobileCard';

const MODALITY_ICONS: Record<string, { label: string; color: string; paths: ReactNode }> = {
  text: {
    label: '文本',
    color: '#0088fe',
    paths: (
      <>
        <path d="M12 4v16" />
        <path d="M4 7V5a1 1 0 0 1 1-1h14a1 1 0 0 1 1 1v2" />
        <path d="M9 20h6" />
      </>
    ),
  },
  file: {
    label: '文件',
    color: '#daa520',
    paths: (
      <>
        <path d="M6 22a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h8a2.4 2.4 0 0 1 1.704.706l3.588 3.588A2.4 2.4 0 0 1 20 8v12a2 2 0 0 1-2 2z" />
        <path d="M14 2v5a1 1 0 0 0 1 1h5" />
      </>
    ),
  },
  image: {
    label: '图像',
    color: '#2e8b57',
    paths: (
      <>
        <rect width="18" height="18" x="3" y="3" rx="2" ry="2" />
        <circle cx="9" cy="9" r="2" />
        <path d="m21 15-3.086-3.086a2 2 0 0 0-2.828 0L6 21" />
      </>
    ),
  },
  audio: {
    label: '音频',
    color: '#7b68ee',
    paths: (
      <>
        <path d="M2 10v3" />
        <path d="M6 6v11" />
        <path d="M10 3v18" />
        <path d="M14 8v7" />
        <path d="M18 5v13" />
        <path d="M22 10v3" />
      </>
    ),
  },
  video: {
    label: '视频',
    color: '#ff4500',
    paths: (
      <>
        <path d="m16 13 5.223 3.482a.5.5 0 0 0 .777-.416V7.87a.5.5 0 0 0-.752-.432L16 10.5" />
        <rect x="2" y="6" width="14" height="12" rx="2" />
      </>
    ),
  },
};

function DirectionRow({
  text,
  direction,
  values,
}: {
  text: string | null;
  direction: string;
  values?: string[];
}) {
  const kinds = normalizedModalities(values);
  if (!text && kinds.length === 0) return null;
  return (
    <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 4, fontSize: 10, marginTop: 2 }}>
      {text ? <span>{text}</span> : null}
      {kinds.map((kind) => {
        const item = MODALITY_ICONS[kind];
        return item ? (
          <Tooltip key={kind} title={t('{direction}：{label}', { direction, label: t(item.label) })}>
            <span
              aria-label={`${direction} ${t(item.label)}`}
              style={{
                display: 'inline-flex',
                alignItems: 'center',
                justifyContent: 'center',
                width: 16,
                height: 16,
                borderRadius: 4,
                color: item.color,
                background: `color-mix(in srgb, ${item.color} 12%, transparent)`,
              }}
            >
              <svg
                width="11"
                height="11"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2.5"
                strokeLinecap="round"
                strokeLinejoin="round"
                aria-hidden="true"
              >
                {item.paths}
              </svg>
            </span>
          </Tooltip>
        ) : null;
      })}
    </div>
  );
}

/** One provider catalog entry rendered like the model list cards. */
export function ModelCard({
  model,
  tags,
  selected,
  onClick,
}: {
  model: ProviderModel;
  tags?: ReactNode;
  selected?: boolean;
  onClick?: () => void;
}) {
  const apis = modelApiLabel(model);
  const rows = modelDirectionRows(model);
  return (
    <div style={{ borderRadius: 10, boxShadow: selected ? '0 0 0 2px #0ea5e9' : undefined }}>
      <MobileCard
        dense
        title={model.name || model.id}
        subtitle={
          <>
            <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 4 }}>
              <Typography.Text type="secondary" style={{ fontSize: 10, wordBreak: 'break-all' }}>
                {model.id}
              </Typography.Text>
              {apis ? (
                <Tag color="blue" style={{ marginInlineEnd: 0, fontSize: 10, lineHeight: '15px', paddingInline: 4 }}>
                  {apis}
                </Tag>
              ) : null}
            </div>
            <DirectionRow text={rows.input} direction={t('输入')} values={model.inputs} />
            <DirectionRow text={rows.output} direction={t('输出')} values={model.outputs} />
          </>
        }
        tags={tags}
        onClick={onClick}
      />
    </div>
  );
}
