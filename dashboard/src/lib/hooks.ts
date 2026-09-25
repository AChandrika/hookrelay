import { useCallback, useEffect, useRef, useState } from "react";
import type { Page } from "../types";

export function toError(e: unknown): Error {
  return e instanceof Error ? e : new Error(String(e));
}

/** Current route from the URL hash: "#/events/123" -> "/events/123". */
export function useHashRoute(): string {
  const [path, setPath] = useState(readHash);
  useEffect(() => {
    const onChange = () => setPath(readHash());
    window.addEventListener("hashchange", onChange);
    return () => window.removeEventListener("hashchange", onChange);
  }, []);
  return path;
}

function readHash(): string {
  return window.location.hash.slice(1) || "/";
}

/**
 * Loads data and optionally re-polls it. Keeps the previous data on screen
 * while refreshing, so polling never flashes a loading state.
 */
export function useAsync<T>(load: () => Promise<T>, deps: unknown[], pollMs?: number) {
  const [data, setData] = useState<T>();
  const [error, setError] = useState<Error>();
  const [tick, setTick] = useState(0);
  const loadRef = useRef(load);
  loadRef.current = load;

  const reload = useCallback(() => setTick((t) => t + 1), []);

  useEffect(() => {
    let alive = true;
    loadRef.current().then(
      (d) => {
        if (!alive) return;
        setData(d);
        setError(undefined);
      },
      (e: unknown) => {
        if (alive) setError(toError(e));
      },
    );
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, tick]);

  useEffect(() => {
    if (!pollMs) return;
    const id = window.setInterval(() => {
      if (!document.hidden) reload();
    }, pollMs);
    return () => window.clearInterval(id);
  }, [pollMs, reload]);

  return { data, error, reload };
}

/** A cursor-paginated list with "load more". */
export function usePaged<T>(fetchPage: (cursor?: string) => Promise<Page<T>>, deps: unknown[]) {
  const [items, setItems] = useState<T[] | null>(null);
  const [cursor, setCursor] = useState<string>();
  const [error, setError] = useState<Error>();
  const [loadingMore, setLoadingMore] = useState(false);
  const fetchRef = useRef(fetchPage);
  fetchRef.current = fetchPage;

  const refresh = useCallback(async () => {
    try {
      const p = await fetchRef.current();
      setItems(p.data);
      setCursor(p.next_cursor);
      setError(undefined);
    } catch (e) {
      setError(toError(e));
    }
  }, []);

  const loadMore = useCallback(async () => {
    if (!cursor) return;
    setLoadingMore(true);
    try {
      const p = await fetchRef.current(cursor);
      setItems((prev) => [...(prev ?? []), ...p.data]);
      setCursor(p.next_cursor);
    } catch (e) {
      setError(toError(e));
    } finally {
      setLoadingMore(false);
    }
  }, [cursor]);

  useEffect(() => {
    setItems(null);
    void refresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refresh, ...deps]);

  return { items, setItems, error, hasMore: Boolean(cursor), loadingMore, loadMore, refresh };
}
