import React from 'react';
import ReactDOM from 'react-dom/client';
import { ConfigProvider, theme } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import enUS from 'antd/locale/en_US';
import 'antd/dist/reset.css';
import '@xyflow/react/dist/style.css';
import './styles.css';
import App from './App';
import { ThemeProvider, useTheme } from './theme';
import { I18nProvider, useI18n } from './i18n';

function ThemedConfigProvider({ children }: { children: React.ReactNode }) {
  const { resolved } = useTheme();
  const { lang } = useI18n();
  return (
    <ConfigProvider
      locale={lang === 'zh-CN' ? zhCN : enUS}
      theme={{ algorithm: resolved === 'dark' ? theme.darkAlgorithm : theme.defaultAlgorithm }}
    >
      {children}
    </ConfigProvider>
  );
}

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <I18nProvider>
      <ThemeProvider>
        <ThemedConfigProvider>
          <App />
        </ThemedConfigProvider>
      </ThemeProvider>
    </I18nProvider>
  </React.StrictMode>,
);
