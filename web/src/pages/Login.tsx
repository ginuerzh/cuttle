import { useState } from 'react';
import { Button, Card, Checkbox, Dropdown, Form, Input, Typography, message } from 'antd';
import { GlobalOutlined } from '@ant-design/icons';
import { login } from '../api/client';
import { LANGUAGES, useI18n, type Lang } from '../i18n';

export function Login({ onSuccess }: { onSuccess: () => void }) {
  const [loading, setLoading] = useState(false);
  const { t, lang, setLang } = useI18n();
  const languageLabel = LANGUAGES.find((item) => item.id === lang)?.label ?? 'English';

  const submit = async (values: { password: string; remember?: boolean }) => {
    setLoading(true);
    try {
      await login(values.password, !!values.remember);
      onSuccess();
    } catch {
      message.error(t('密码错误'));
    } finally {
      setLoading(false);
    }
  };

  return (
    <div
      className="gw-login"
      style={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        background: 'var(--gw-login-bg)',
      }}
    >
      <Card style={{ width: '100%', maxWidth: 360, margin: '0 16px' }}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8 }}>
          <Typography.Title level={4} style={{ margin: 0 }}>
            llm-gw
          </Typography.Title>
          <Dropdown
            trigger={['click']}
            placement="bottomRight"
            menu={{
              items: LANGUAGES.map((item) => ({ key: item.id, label: item.label })),
              selectable: true,
              selectedKeys: [lang],
              onClick: ({ key }) => setLang(key as Lang),
            }}
          >
            <Button size="small" icon={<GlobalOutlined />}>
              {languageLabel}
            </Button>
          </Dropdown>
        </div>
        <Typography.Paragraph type="secondary">{t('单管理员控制台')}</Typography.Paragraph>
        <Form layout="vertical" onFinish={submit} initialValues={{ remember: true }}>
          <Form.Item name="password" label={t('密码')} rules={[{ required: true }]}>
            <Input.Password autoFocus />
          </Form.Item>
          <Form.Item name="remember" valuePropName="checked" style={{ marginBottom: 12 }}>
            <Checkbox>{t('记住我（30 天，重启后仍有效）')}</Checkbox>
          </Form.Item>
          <Button type="primary" htmlType="submit" block loading={loading}>
            {t('登录')}
          </Button>
        </Form>
      </Card>
    </div>
  );
}
