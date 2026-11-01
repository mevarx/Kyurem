import { useEffect, useState } from "react";

/** localStorage key for the dashboard's last-visit timestamp. */
const LAST_VISIT_KEY = "kyurem.dashboard.lastVisit";

/** Read the stored last-visit timestamp without touching storage. */
function readLastVisit(): string | null {
  try {
    return window.localStorage.getItem(LAST_VISIT_KEY);
  } catch {
    return null;
  }
}

/**
 * Last-visit timestamp for "since you left" summaries.
 *
 * Returns the timestamp recorded by the previous visit (an ISO string, or
 * `null` on the first visit) and records the current visit. Unavailable
 * storage (private mode, blocked access) degrades to `null`.
 */
export function useLastVisit(): string | null {
  const [lastVisit] = useState<string | null>(readLastVisit);

  useEffect(() => {
    try {
      window.localStorage.setItem(LAST_VISIT_KEY, new Date().toISOString());
    } catch {
      // Storage unavailable: summaries still render, just without a baseline.
    }
  }, []);

  return lastVisit;
}
