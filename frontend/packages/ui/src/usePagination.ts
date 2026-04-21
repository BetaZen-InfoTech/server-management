import { useCallback, useEffect, useState } from "react";
import { DEFAULT_PAGE_SIZES } from "./Table";

// usePagination is the one-liner every list page uses to drive the
// shared Table's pagination footer. Persists the per-page choice in
// localStorage under `pp:<storageKey>` so an operator who picks 250
// once doesn't have to re-pick it every time they open the page.
//
// `total` updates as soon as the API call completes (call setTotal in
// the fetch effect). When the user changes the per-page selector we
// snap back to page 1 so they don't land on an out-of-range page.
export function usePagination(storageKey: string, defaultLimit = 50) {
  const lsKey = `pp:${storageKey}`;
  const [page, setPage] = useState(1);
  const [limit, setLimitState] = useState<number>(() => {
    if (typeof window === "undefined") return defaultLimit;
    const raw = window.localStorage.getItem(lsKey);
    const parsed = raw ? parseInt(raw, 10) : NaN;
    return DEFAULT_PAGE_SIZES.includes(parsed) ? parsed : defaultLimit;
  });
  const [total, setTotal] = useState(0);

  useEffect(() => {
    if (typeof window === "undefined") return;
    window.localStorage.setItem(lsKey, String(limit));
  }, [lsKey, limit]);

  const setLimit = useCallback((n: number) => {
    setLimitState(n);
    setPage(1);
  }, []);

  return { page, setPage, limit, setLimit, total, setTotal };
}
