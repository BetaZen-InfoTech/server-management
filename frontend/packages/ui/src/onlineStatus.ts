import { useEffect, useState } from "react";

// Module-level error class so callers (axios interceptors, login form
// submit handlers, anywhere a fetch is about to fire) can recognise
// the offline-block error without string-matching the message.
//   try { ... } catch (e) {
//     if (e instanceof OfflineError) showBanner();
//   }
export class OfflineError extends Error {
  readonly isOfflineError = true;
  constructor(message = "Network is offline. Reconnect and try again.") {
    super(message);
    this.name = "OfflineError";
  }
}

// useOnlineStatus subscribes to the browser's online / offline events
// and returns a live boolean. Initial value reads navigator.onLine
// synchronously so first render is correct (no flicker on slow JS).
//
// Caveat: navigator.onLine is best-effort. The browser only flips it
// when the OS reports a network adapter change — it does NOT verify
// that the panel's backend is actually reachable. A captive portal
// will still report onLine=true. For a stricter check the consumer
// can run a periodic /api/v1/version probe; the hook leaves that as
// an opt-in (see usePingableServer below).
export function useOnlineStatus(): boolean {
  const [online, setOnline] = useState<boolean>(() =>
    typeof navigator === "undefined" ? true : navigator.onLine
  );
  useEffect(() => {
    if (typeof window === "undefined") return;
    const onOnline = () => setOnline(true);
    const onOffline = () => setOnline(false);
    window.addEventListener("online", onOnline);
    window.addEventListener("offline", onOffline);
    return () => {
      window.removeEventListener("online", onOnline);
      window.removeEventListener("offline", onOffline);
    };
  }, []);
  return online;
}

// usePingableServer is the strict variant — runs a periodic GET against
// the supplied URL (usually `/api/v1/version`, a public no-auth endpoint)
// and exposes `reachable` in addition to navigator.onLine. Pings are
// suspended while the tab is in the background (visibilitychange) so a
// hidden tab doesn't keep the device's radio awake.
export function usePingableServer(
  url: string,
  intervalMs = 30_000
): { online: boolean; reachable: boolean } {
  const online = useOnlineStatus();
  const [reachable, setReachable] = useState(true);
  useEffect(() => {
    if (!online) {
      setReachable(false);
      return;
    }
    let cancelled = false;
    const ping = async () => {
      if (document.visibilityState === "hidden") return;
      try {
        const ctrl = new AbortController();
        const t = window.setTimeout(() => ctrl.abort(), 5_000);
        const res = await fetch(url, { method: "GET", cache: "no-store", signal: ctrl.signal });
        window.clearTimeout(t);
        if (!cancelled) setReachable(res.ok);
      } catch {
        if (!cancelled) setReachable(false);
      }
    };
    ping();
    const id = window.setInterval(ping, intervalMs);
    return () => {
      cancelled = true;
      window.clearInterval(id);
    };
  }, [online, url, intervalMs]);
  return { online, reachable };
}

// useInstallPrompt captures the `beforeinstallprompt` event so a panel
// page can render a "Install app" button on Chrome / Edge / modern
// Android browsers. iOS Safari doesn't fire this event — install on
// iOS goes through the Share sheet → Add to Home Screen, which the UI
// can't trigger programmatically. Returns:
//   - canInstall: true when the browser has fired beforeinstallprompt
//     AND the user hasn't yet installed / dismissed
//   - promptInstall(): triggers the native install dialog. After the
//     user accepts or dismisses, canInstall flips back to false
//     (browsers fire beforeinstallprompt only once per install state).
//   - isStandalone: true when the page is already running as an
//     installed PWA (display-mode: standalone), so the UI can hide the
//     install button entirely.
type BeforeInstallPromptEvent = Event & {
  prompt: () => Promise<void>;
  userChoice: Promise<{ outcome: "accepted" | "dismissed" }>;
};

export function useInstallPrompt(): {
  canInstall: boolean;
  isStandalone: boolean;
  promptInstall: () => Promise<"accepted" | "dismissed" | "unavailable">;
} {
  const [evt, setEvt] = useState<BeforeInstallPromptEvent | null>(null);
  const [isStandalone, setIsStandalone] = useState<boolean>(() => {
    if (typeof window === "undefined" || !window.matchMedia) return false;
    return window.matchMedia("(display-mode: standalone)").matches;
  });
  useEffect(() => {
    if (typeof window === "undefined") return;
    const onBefore = (e: Event) => {
      e.preventDefault();
      setEvt(e as BeforeInstallPromptEvent);
    };
    const onInstalled = () => {
      setEvt(null);
      setIsStandalone(true);
    };
    window.addEventListener("beforeinstallprompt", onBefore);
    window.addEventListener("appinstalled", onInstalled);
    return () => {
      window.removeEventListener("beforeinstallprompt", onBefore);
      window.removeEventListener("appinstalled", onInstalled);
    };
  }, []);
  const promptInstall = async (): Promise<"accepted" | "dismissed" | "unavailable"> => {
    if (!evt) return "unavailable";
    await evt.prompt();
    const { outcome } = await evt.userChoice;
    setEvt(null);
    return outcome;
  };
  return { canInstall: !!evt && !isStandalone, isStandalone, promptInstall };
}
