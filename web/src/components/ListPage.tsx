import type { ReactNode } from 'react';
import { useIsMobile } from './useIsMobile';

/** Single-column list shell: narrow on phones, wider on desktop/tablet. */
export function ListPage({ children }: { children: ReactNode }) {
  const isMobile = useIsMobile();
  return (
    <div
      style={{
        padding: isMobile ? 12 : 20,
        width: '100%',
        maxWidth: 1200,
        margin: '0 auto',
        overflowX: 'hidden',
      }}
    >
      {children}
    </div>
  );
}
