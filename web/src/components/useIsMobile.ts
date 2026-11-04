import { Grid } from 'antd';

/**
 * True on phones / narrow windows. Uses `=== false` so the first render
 * (before antd measures the viewport) stays on the desktop layout instead of
 * flashing the mobile one.
 */
export function useIsMobile(): boolean {
  const screens = Grid.useBreakpoint();
  return screens.md === false;
}
