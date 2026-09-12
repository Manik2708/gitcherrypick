// Reads and writes, with the three states a screen actually renders.
//
// A read owns loading/error/data and aborts on unmount, so a screen left during
// a slow request does not set state on a dead component. A write is explicit:
// nothing fires until something is clicked.

import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError } from "../api/http";

export interface Async<T> {
  data: T | undefined;
  loading: boolean;
  error: ApiError | null;
  reload: () => void;
}

function asApiError(cause: unknown): ApiError {
  if (cause instanceof ApiError) return cause;
  const message = cause instanceof Error ? cause.message : "The request failed.";
  return new ApiError(0, "network_error", message, {});
}

export function useAsync<T>(
  run: (signal: AbortSignal) => Promise<T>,
  deps: unknown[],
  enabled = true,
): Async<T> {
  const [data, setData] = useState<T | undefined>(undefined);
  const [loading, setLoading] = useState(enabled);
  const [error, setError] = useState<ApiError | null>(null);
  const [nonce, setNonce] = useState(0);

  // The runner is held in a ref so an inline closure does not re-fire the read
  // on every render; `deps` is what decides that.
  const latest = useRef(run);
  latest.current = run;

  useEffect(() => {
    if (!enabled) {
      setLoading(false);
      return;
    }
    const controller = new AbortController();
    let live = true;

    setLoading(true);
    setError(null);

    latest
      .current(controller.signal)
      .then((value) => {
        if (live) setData(value);
      })
      .catch((cause: unknown) => {
        if (!live || controller.signal.aborted) return;
        setError(asApiError(cause));
      })
      .finally(() => {
        if (live) setLoading(false);
      });

    return () => {
      live = false;
      controller.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, enabled, nonce]);

  const reload = useCallback(() => setNonce((n) => n + 1), []);

  return { data, loading, error, reload };
}

export interface Action<A extends unknown[], T> {
  run: (...args: A) => Promise<T | null>;
  data: T | undefined;
  pending: boolean;
  error: ApiError | null;
}

export function useAction<A extends unknown[], T>(
  run: (...args: A) => Promise<T>,
): Action<A, T> {
  const [data, setData] = useState<T | undefined>(undefined);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);

  const latest = useRef(run);
  latest.current = run;

  const fire = useCallback(async (...args: A): Promise<T | null> => {
    setPending(true);
    setError(null);
    try {
      const value = await latest.current(...args);
      setData(value);
      return value;
    } catch (cause: unknown) {
      setError(asApiError(cause));
      return null;
    } finally {
      setPending(false);
    }
  }, []);

  return { run: fire, data, pending, error };
}
