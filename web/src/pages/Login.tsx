import { useState } from 'react';
import { Button, Card, Checkbox, Form, Input, Typography, message } from 'antd';
import { login } from '../api/client';

export function Login({ onSuccess }: { onSuccess: () => void }) {
  const [loading, setLoading] = useState(false);

  const submit = async (values: { password: string; remember?: boolean }) => {
    setLoading(true);
    try {
      await login(values.password, !!values.remember);
      onSuccess();
    } catch {
      message.error('密码错误');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div
      style={{
        minHeight: '100vh',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        background: '#f1f5f9',
      }}
    >
      <Card style={{ width: '100%', maxWidth: 360, margin: '0 16px' }}>
        <Typography.Title level={4} style={{ marginTop: 0 }}>
          llm-gw
        </Typography.Title>
        <Typography.Paragraph type="secondary">单管理员控制台</Typography.Paragraph>
        <Form layout="vertical" onFinish={submit} initialValues={{ remember: true }}>
          <Form.Item name="password" label="密码" rules={[{ required: true }]}>
            <Input.Password autoFocus />
          </Form.Item>
          <Form.Item name="remember" valuePropName="checked" style={{ marginBottom: 12 }}>
            <Checkbox>记住我（30 天，重启后仍有效）</Checkbox>
          </Form.Item>
          <Button type="primary" htmlType="submit" block loading={loading}>
            登录
          </Button>
        </Form>
      </Card>
    </div>
  );
}
