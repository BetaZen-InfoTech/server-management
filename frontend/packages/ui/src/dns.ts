// Shared DNS helpers used by the WHM and User Panel DNS Records editors.
// Centralised here so the cPanel-style FQDN auto-normalisation, label
// validation, type catalogue, and TTL defaults stay identical across
// both panels — drift would mean a record edited from the WHM side
// could fail validation on the User Panel side and vice versa.

// RECORD_TYPES is the full superset cPanel exposes in its "Add Record"
// dropdown. Authoring aliases (DMARC, SPF) resolve server-side to TXT.
// Order matters — the picker renders this list verbatim and operators
// expect it sorted as they see it in cPanel.
export const RECORD_TYPES = [
  "A",
  "AAAA",
  "AFSDB",
  "CAA",
  "CNAME",
  "DMARC",
  "DNAME",
  "DS",
  "HINFO",
  "HTTPS",
  "LOC",
  "MX",
  "NAPTR",
  "NS",
  "PTR",
  "RP",
  "SRV",
  "TXT",
] as const;

export type RecordType = (typeof RECORD_TYPES)[number];

// FILTER_TYPES is the smaller chip set we surface above the records
// table — the long tail (AFSDB, HINFO, RP, …) lives behind a "More"
// dropdown. Mirrors the cPanel filter row in the user's screenshots.
export const FILTER_TYPES = ["A", "AAAA", "CNAME", "MX", "NS", "SOA", "SRV", "TXT"] as const;

// defaultTTLFor returns the cPanel-style default TTL for a record type.
// A/AAAA default to 60 because IPs are the most likely thing to change
// quickly during an IP migration and a long cache there hurts cutover.
// Everything else gets the historic 3600 (1 hour) default. Mirrored
// server-side in dns_service.go::defaultTTLFor — keep them in sync.
export function defaultTTLFor(rtype: string): number {
  if (rtype === "A" || rtype === "AAAA") return 60;
  return 3600;
}

// minTTLFor is the floor the UI enforces on the TTL input. Lower than
// 60 is rarely useful (many resolvers clamp to 60 anyway) and a TTL of
// 0 confuses BIND-derived zones. The user spec asks for 60 minimum on
// A specifically; we apply the same floor across all types for
// simplicity — a 60s minimum never hurts.
export function minTTLFor(_rtype: string): number {
  return 60;
}

// normalizeFqdn implements the cPanel "Zone Name auto-completion" rule
// the user spelled out:
//
//   domain = abc.com
//   "app"           → "app.abc.com."   (relative → append ".<domain>.")
//   "app.abc.com."  → "app.abc.com."   (already absolute, leave alone)
//   "app.abc."      → "app.abc.abc.com." (the cPanel quirk — trailing
//                                          dot alone doesn't make it
//                                          absolute; only the full
//                                          ".<domain>." suffix does)
//
// The rule is single-line: if the input doesn't already end with
// ".<domain>." (or IS exactly "<domain>." for the apex), append
// ".<domain>." to it. Apex shorthand "@" stays "@" — operators
// reading PowerDNS / BIND zonefiles expect that token.
export function normalizeFqdn(input: string, domain: string): string {
  const s = input.trim();
  if (!s) return s;
  if (s === "@") return s;
  const d = domain.trim().replace(/\.+$/, ""); // drop any trailing dot on the zone domain
  if (!d) return s;
  // Apex form already with trailing dot.
  if (s === d || s === d + ".") return d + ".";
  const suffix = "." + d + ".";
  if (s.endsWith(suffix)) return s;
  return s + suffix;
}

// LABEL_RE matches one DNS label: letters, digits, hyphen, underscore.
// Wildcard (`*`) is allowed as its own standalone label per RFC 4592 —
// every other character is rejected client-side so the server doesn't
// have to render a less-specific error from PowerDNS.
const LABEL_RE = /^([A-Za-z0-9_-]+|\*)$/;

export interface LabelValidation {
  ok: boolean;
  error?: string;
}

// validateZoneName runs the per-label validation cPanel surfaces
// inline ("A DNS label must not be empty" / "must contain only the
// following characters: a-z, A-Z, 0-9, -, _."). Apex shorthand ("@")
// and the bare zone domain are treated as valid. Trailing dot is
// stripped before splitting so "app.example.com." validates the same
// as "app.example.com".
export function validateZoneName(input: string): LabelValidation {
  const s = input.trim();
  if (!s) return { ok: false, error: "A DNS label must not be empty." };
  if (s === "@") return { ok: true };
  // Strip exactly one trailing dot — the FQDN terminator.
  const stripped = s.endsWith(".") ? s.slice(0, -1) : s;
  if (!stripped) return { ok: false, error: "A DNS label must not be empty." };
  const labels = stripped.split(".");
  for (const lbl of labels) {
    if (!lbl) return { ok: false, error: "A DNS label must not be empty." };
    if (!LABEL_RE.test(lbl)) {
      return {
        ok: false,
        error:
          "The DNS label must contain only the following characters: a-z, A-Z, 0-9, -, and _.",
      };
    }
  }
  return { ok: true };
}

// RECORD_HELP gives placeholder + inline help text per type so the
// inline editor's Value column always tells the operator what it
// expects. Keep these terse — the input is narrow.
export const RECORD_HELP: Record<string, { placeholder: string; hint: string }> = {
  A: { placeholder: "192.168.1.50", hint: "IPv4 address." },
  AAAA: { placeholder: "2001:db8::1", hint: "IPv6 address." },
  AFSDB: { placeholder: "1 hostname.example.com.", hint: "AFS subtype + hostname." },
  CAA: { placeholder: '0 issue "letsencrypt.org"', hint: "Flags + tag + value." },
  CNAME: { placeholder: "target.example.com.", hint: "Target hostname (FQDN)." },
  DMARC: {
    placeholder: "v=DMARC1; p=none; rua=mailto:admin@example.com",
    hint: "DMARC policy (stored as TXT).",
  },
  DNAME: { placeholder: "target.example.com.", hint: "Subtree alias target." },
  DS: { placeholder: "12345 7 1 ABCDEF...", hint: "DNSSEC delegation signer." },
  HINFO: { placeholder: '"Linux" "x86_64"', hint: "Host info (CPU, OS)." },
  HTTPS: { placeholder: "1 . alpn=h2,h3", hint: "HTTPS service binding (RFC 9460)." },
  LOC: { placeholder: "37 23 30 N 122 8 30 W 0m", hint: "Geographic location." },
  MX: { placeholder: "mail.example.com.", hint: "Mail server hostname (priority in its own field)." },
  NAPTR: { placeholder: '100 10 "S" "..." "" target.', hint: "Naming Authority Pointer." },
  NS: { placeholder: "ns1.example.com.", hint: "Authoritative nameserver." },
  PTR: { placeholder: "host.example.com.", hint: "Reverse-DNS target." },
  RP: { placeholder: "admin.example.com. txt-rec.example.com.", hint: "Responsible Person." },
  SRV: { placeholder: "10 60 5060 sip.example.com.", hint: "priority weight port target." },
  TXT: { placeholder: "v=spf1 include:_spf.google.com ~all", hint: "Free-form text." },
};

// PRIORITY_TYPES is the set where the priority field is populated as
// its own column. MX/SRV have a real priority; CAA reuses the field
// for "flags". Everything else hides the column.
export const PRIORITY_TYPES = new Set(["MX", "SRV", "CAA"]);
