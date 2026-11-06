import { useEffect, useRef } from 'react';
import { getConfig, UnauthorizedError } from './api/client';
import { eventsPath, parseLiveMessage, type SnapshotMessage } from './api/events';

export interface LiveGraphOptions {
  enabled: boolean;
  range: string;
  resetKey: number;
  sessionKey: number;
  onSnapshot: (msg: SnapshotMessage) => void;
  onStatus: (connected: boolean) => void;
  onAuthError: () => void;
}

type Mode = { kind: 'range'; range: string } | { kind: 'since'; since: string } | { kind: 'reset' };

/**
 * Streams topology snapshots over /api/events.
 *
 * The subscription is fixed for the life of one socket. A range change or a
 * reset rebuilds the URL; a network reconnect resumes with the last server
 * provided `since`.
 */
export function useLiveGraph(opts: LiveGraphOptions): void {
  const onSnapshot = useRef(opts.onSnapshot);
  const onStatus = useRef(opts.onStatus);
  const onAuthError = useRef(opts.onAuthError);
  onSnapshot.current = opts.onSnapshot;
  onStatus.current = opts.onStatus;
  onAuthError.current = opts.onAuthError;

  const mode = useRef<Mode>({ kind: 'range', range: opts.range });
  const previousRange = useRef(opts.range);
  const previousReset = useRef(opts.resetKey);
  const previousSession = useRef(opts.sessionKey);

  useEffect(() => {
    if (previousSession.current !== opts.sessionKey) {
      previousSession.current = opts.sessionKey;
      mode.current = { kind: 'range', range: opts.range };
    }
    if (previousRange.current !== opts.range) {
      previousRange.current = opts.range;
      mode.current = { kind: 'range', range: opts.range };
    }
    if (previousReset.current !== opts.resetKey) {
      previousReset.current = opts.resetKey;
      mode.current = { kind: 'reset' };
    }
    if (!opts.enabled) {
      return;
    }

    let disposed = false;
    let socket: WebSocket | null = null;
    let retryTimer: number | undefined;
    let attempt = 0;

    const subscriptionPath = () => {
      const current = mode.current;
      if (current.kind === 'reset') {
        return eventsPath({ reset: true });
      }
      if (current.kind === 'since') {
        return eventsPath({ since: current.since });
      }
      return eventsPath({ range: current.range });
    };

    const handleAuthError = () => {
      mode.current = { kind: 'range', range: opts.range };
      onAuthError.current();
    };

    function schedule() {
      if (disposed) {
        return;
      }
      const base = Math.min(30_000, 1_000 * 2 ** attempt);
      attempt++;
      const delay = base * (0.8 + Math.random() * 0.4);
      retryTimer = window.setTimeout(() => {
        retryTimer = undefined;
        connect();
      }, delay);
    }

    function connect() {
      if (disposed) {
        return;
      }
      const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
      const current = new WebSocket(`${protocol}//${window.location.host}${subscriptionPath()}`);
      socket = current;

      current.onmessage = (event) => {
        if (disposed || socket !== current) {
          return;
        }
        const msg = parseLiveMessage(String(event.data));
        if (!msg) {
          return;
        }
        if (msg.type === 'error') {
          onStatus.current(false);
          return;
        }
        mode.current = { kind: 'since', since: msg.since };
        attempt = 0;
        onSnapshot.current(msg);
        onStatus.current(true);
      };

      current.onerror = () => {
        if (!disposed && socket === current) {
          onStatus.current(false);
        }
      };

      current.onclose = (event) => {
        if (disposed || socket !== current) {
          return;
        }
        socket = null;
        onStatus.current(false);
        if (event.code === 4401) {
          handleAuthError();
          return;
        }
        if (event.code === 4408) {
          mode.current = { kind: 'range', range: opts.range };
          attempt = 0;
          schedule();
          return;
        }
        void getConfig()
          .then(() => {
            if (!disposed) {
              schedule();
            }
          })
          .catch((err) => {
            if (disposed) {
              return;
            }
            if (err instanceof UnauthorizedError) {
              handleAuthError();
            } else {
              schedule();
            }
          });
      };
    }

    function close() {
      if (retryTimer !== undefined) {
        window.clearTimeout(retryTimer);
        retryTimer = undefined;
      }
      if (!socket) {
        return;
      }
      const current = socket;
      socket = null;
      current.onmessage = null;
      current.onerror = null;
      current.onclose = null;
      current.close();
    }

    const onVisibility = () => {
      if (document.hidden) {
        close();
        return;
      }
      if (!socket) {
        attempt = 0;
        connect();
      }
    };

    document.addEventListener('visibilitychange', onVisibility);
    if (!document.hidden) {
      connect();
    }

    return () => {
      disposed = true;
      document.removeEventListener('visibilitychange', onVisibility);
      close();
    };
  }, [opts.enabled, opts.range, opts.resetKey, opts.sessionKey]);
}
