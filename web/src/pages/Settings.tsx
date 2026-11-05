import { useState } from 'react';
import { Alert, Button, Descriptions, Divider, Form, Input, Typography, message } from 'antd';
import { changePassword } from '../api/client';
import { translate as t } from '../i18n';
import type { ConfigResponse } from '../api/types';

export function SettingsPage({ config, onLogout }: { config: ConfigResponse; onLogout: () => void }) {
  const [busy, setBusy] = useState(false);
  const [form] = Form.useForm();

  const submit = async (v: { oldPassword: string; newPassword: string }) => {
    setBusy(true);
    try {
      await changePassword(v.oldPassword, v.newPassword);
      message.success(t('密码已更新，请重新登录'));
      onLogout();
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div style={{ padding: 20, width: '100%', overflow: 'auto', maxWidth: 760 }}>
      <Typography.Title level={4} style={{ marginTop: 0 }}>
        {t('设置')}
      </Typography.Title>

      <Descriptions size="small" column={1} bordered>
        <Descriptions.Item label={t('数据面监听')}>{config.gateway.listen}</Descriptions.Item>
        <Descriptions.Item label={t('管理端监听')}>{config.admin.listen}</Descriptions.Item>
        <Descriptions.Item label={t('stats 路径')}>{config.stats?.path || '—'}</Descriptions.Item>
        <Descriptions.Item label={t('明细保留')}>{config.stats?.retainRequests || t('默认 168h')}</Descriptions.Item>
        <Descriptions.Item label={t('聚合保留')}>{config.stats?.retainRollups || t('默认 2160h')}</Descriptions.Item>
      </Descriptions>

      <Divider orientation="left">{t('修改密码')}</Divider>
      <Alert type="info" showIcon message={t('修改成功后当前会话会失效，需要用新密码重新登录。')} style={{ marginBottom: 12 }} />
      <Form form={form} layout="vertical" style={{ maxWidth: 360 }} onFinish={submit}>
        <Form.Item name="oldPassword" label={t('当前密码')} rules={[{ required: true }]}>
          <Input.Password />
        </Form.Item>
        <Form.Item name="newPassword" label={t('新密码（至少 8 位）')} rules={[{ required: true, min: 8 }]}>
          <Input.Password />
        </Form.Item>
        <Button type="primary" htmlType="submit" loading={busy}>
          {t('保存')}
        </Button>
      </Form>
    </div>
  );
}
