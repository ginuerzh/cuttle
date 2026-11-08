import { Badge, Button, Select, Space, Switch, Tag } from 'antd';
import { EditOutlined, ReloadOutlined } from '@ant-design/icons';
import { translate as t } from '../i18n';

export interface OverviewRange {
  label: string;
  value: string;
}

export interface OverviewToolbarProps {
  mode: 'monitor' | 'edit';
  onMode: (mode: 'monitor' | 'edit') => void;
  range: string;
  onRange: (range: string) => void;
  ranges: readonly OverviewRange[];
  stale: boolean;
  updatedAt: string;
  sinceAt: string;
  onReset: () => void;
  isMobile: boolean;
  animate: boolean;
  onAnimate: (animate: boolean) => void;
}

/** Overview toolbar: one edit toggle on the left, time range at the right edge. */
export function OverviewToolbar({
  mode,
  onMode,
  range,
  onRange,
  ranges,
  stale,
  updatedAt,
  sinceAt,
  onReset,
  isMobile,
  animate,
  onAnimate,
}: OverviewToolbarProps) {
  const editing = mode === 'edit';
  return (
    <div
      style={{
        display: 'flex',
        flexWrap: 'wrap',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: 8,
        padding: '8px 12px',
        background: 'var(--gw-surface)',
        borderBottom: '1px solid var(--gw-border)',
      }}
    >
      <Button
        type={editing ? 'primary' : 'default'}
        icon={<EditOutlined />}
        aria-pressed={editing}
        onClick={() => onMode(editing ? 'monitor' : 'edit')}
      >
        {editing ? t('退出编辑') : t('编辑')}
      </Button>
      <Space wrap size={8}>
        {editing ? (
          <Tag color="orange">{t('编辑模式 · 统计已暂停')}</Tag>
        ) : (
          <>
            {!isMobile ? (
              <Badge
                status={stale ? 'warning' : 'success'}
                text={
                  stale
                    ? t('数据延迟')
                    : updatedAt
                      ? t('更新 {time} · 统计自 {since}', {
                          time: updatedAt,
                          since: sinceAt || '—',
                        })
                      : t('等待数据')
                }
              />
            ) : null}
            <Space size={4}>
              <span>{t('动画')}</span>
              <Switch size="small" checked={animate} onChange={onAnimate} aria-label={t('动画')} />
            </Space>
            <Select
              size="small"
              value={range}
              onChange={onRange}
              options={ranges.map(({ label, value }) => ({ label: t(label), value }))}
              style={{ width: 96 }}
            />
            <Button
              size="small"
              icon={<ReloadOutlined />}
              onClick={onReset}
              title={t('重置统计')}
            >
              {t('重置统计')}
            </Button>
          </>
        )}
      </Space>
    </div>
  );
}
