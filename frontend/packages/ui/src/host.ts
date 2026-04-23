// Host-derived helpers used by the auth pages so placeholders + example
// emails reflect the panel's actual hostname instead of a hardcoded
// "admin@serverpanel.io" / "you@example.com" string. Operators on
// panel.example.com should see admin@panel.example.com — and operators
// hitting the panel by IP fall back to a generic example domain
// because admin@<ip-address> isn't a useful hint.

// IPv4 / IPv6 detection. We don't want to compose an email like
// admin@187.127.155.209 — that's not a real address shape and confuses
// the operator. Single-label hosts (e.g. "localhost", "ubuntu") get
// the same fallback for the same reason.
const IPV4_RE = /^\d{1,3}(?:\.\d{1,3}){3}$/;
function isHostnameUsableForEmail(host: string): boolean {
  const h = host.trim().toLowerCase();
  if (!h) return false;
  if (h === "localhost") return false;
  if (IPV4_RE.test(h)) return false;
  // IPv6 literals in URLs come wrapped in [...]; reject any colon-bearing
  // host so we don't end up with admin@[2001:db8::1].
  if (h.includes(":")) return false;
  // FQDN must contain at least one dot (TLD separator) to be a valid
  // email host part.
  if (!h.includes(".")) return false;
  return true;
}

// hostFromBrowser returns window.location.hostname when the panel is
// being served over HTTP/HTTPS, or "" in non-browser contexts (SSR /
// tests). Centralised so callers don't have to typeof-check window.
export function hostFromBrowser(): string {
  if (typeof window === "undefined" || !window.location) return "";
  return window.location.hostname || "";
}

// emailForLocal returns "<local>@<panel-host>" when the browser is on
// a real FQDN, and "<local>@example.com" otherwise. Used as the
// placeholder on every email-input field where we want the operator
// to "see" their own hostname stamped in — works for admin@ on the
// WHM login, you@ on the cPanel login, vendor@ on the SSL issue
// modal, and so on.
export function emailForLocal(localPart: string, fallback = "example.com"): string {
  const host = hostFromBrowser();
  if (isHostnameUsableForEmail(host)) {
    return `${localPart}@${host}`;
  }
  return `${localPart}@${fallback}`;
}

// adminEmailPlaceholder is the most common convenience wrapper —
// every WHM-side admin form expects an admin@... address. cPanel-side
// pages should call emailForLocal("you") or pass their own local part.
export function adminEmailPlaceholder(): string {
  return emailForLocal("admin");
}
