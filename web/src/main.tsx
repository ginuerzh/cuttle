import React from 'react';
import ReactDOM from 'react-dom/client';
import { ConfigProvider, theme } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import 'antd/dist/reset.css';
import '@xyflow/react/dist/style.css';
import './styles.css';
import App from './App';
import { ThemeProvider, useTheme } from './theme';

function ThemedConfigProvider({ children }: { children: React.ReactNode }) {
  const { resolved } = useTheme();
  return (
    <ConfigProvider
      locale={zhCN}
      theme={{ algorithm: resolved === 'dark' ? theme.darkAlgorithm : theme.defaultAlgorithm }}
    >
      {children}
    </ConfigProvider>
  );
}

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <ThemeProvider>
      <ThemedConfigProvider>
        <App />
      </ThemedConfigProvider>
    </ThemeProvider>
  </React.StrictMode>,
);
