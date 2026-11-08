export const ANIMATION_STORAGE_KEY = 'cuttle.animation';

/** Only the literal `0` disables animations; missing or corrupt values default on. */
export function normalizeAnimationEnabled(raw: string | null | undefined): boolean {
  return raw !== '0';
}

export function loadAnimationEnabled(): boolean {
  try {
    return normalizeAnimationEnabled(window.localStorage.getItem(ANIMATION_STORAGE_KEY));
  } catch {
    return true;
  }
}

export function saveAnimationEnabled(enabled: boolean): void {
  try {
    window.localStorage.setItem(ANIMATION_STORAGE_KEY, enabled ? '1' : '0');
  } catch {
    // Storage can be unavailable (private mode, blocked cookies); the live
    // state still changes for this session.
  }
}
