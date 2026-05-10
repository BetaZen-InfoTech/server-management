import { useEffect, useState } from "react";
import { Card, Button } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  CheckCircle2,
  AlertTriangle,
  XCircle,
  RefreshCw,
  Wrench,
  ChevronDown,
  ChevronUp,
  Mail,
  Server,
  Plug,
  Settings,
  Terminal,
} from "lucide-react";

// MailIssuesPage is the operator-facing mirror of
// scripts/_diag_mail_stack.py — same checks, same auto-heals, but
// surfaced inside the WHM panel so an operator who notices "Roundcube
// can't connect to localhost:143" doesn't have to SSH in.
//
// Layout:
//   • header summary (PASS / WARN / FAIL counts) + Refresh button +
//     "Auto-fix all safe issues" button when there's at least one
//     fail/warn marked auto_fixable.
//   • per-group cards (Packages / Services / Ports / Configuration /
//     Tooling). Each row: status icon + label + (collapsible) detail
//     with Symptom + Resolution playbook + per-row Fix button.
//   • a static "Common Symptoms → Resolution" knowledge-base section
//     at the bottom that maps user-visible symptoms (e.g. "webmail
//     shows 503") to the right diagnostic check + manual playbook.

interface Check {
  id: string;
  group: string;
  label: string;
  status: "pass" | "warn" | "fail";
  problem_type?: string;
  symptom?: string;
  detail?: string;
  fix_hint?: string;
  fix_command?: string;
  resolution?: string[];
  auto_fixable: boolean;
}

interface DiagnosticReport {
  generated_at: string;
  checks: Check[];
  summary: { pass: number; warn: number; fail: number };
}

interface FixResult {
  id: string;
  command: string;
  success: boolean;
  output?: string;
}

const GROUP_ICONS: Record<string, any> = {
  Packages: Server,
  Services: Settings,
  Ports: Plug,
  Configuration: Wrench,
  Tooling: Terminal,
};

function StatusIcon({ status }: { status: Check["status"] }) {
  if (status === "pass") return <CheckCircle2 size={16} className="text-emerald-400 shrink-0" />;
  if (status === "warn") return <AlertTriangle size={16} className="text-amber-400 shrink-0" />;
  return <XCircle size={16} className="text-red-400 shrink-0" />;
}

function CheckRow({
  check,
  fixing,
  onFix,
}: {
  check: Check;
  fixing: boolean;
  onFix: (id: string) => void;
}) {
  const [open, setOpen] = useState(check.status !== "pass");
  const hasDetail =
    !!check.symptom || !!check.detail || (check.resolution && check.resolution.length > 0) || !!check.fix_command;

  return (
    <div
      className={`border-b border-panel-border last:border-b-0 ${
        check.status === "fail"
          ? "bg-red-500/5"
          : check.status === "warn"
          ? "bg-amber-500/5"
          : ""
      }`}
    >
      <div className="px-4 py-3 flex items-center gap-3">
        <StatusIcon status={check.status} />
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <span className="text-sm font-medium text-panel-text truncate">{check.label}</span>
            {check.problem_type && check.status !== "pass" && (
              <span className="px-1.5 py-0.5 text-[10px] uppercase tracking-wide rounded bg-panel-bg border border-panel-border text-panel-muted">
                {check.problem_type.replace(/_/g, " ")}
              </span>
            )}
          </div>
          {check.fix_hint && check.status !== "pass" && (
            <div className="text-xs text-panel-muted mt-0.5 truncate">{check.fix_hint}</div>
          )}
        </div>
        {check.auto_fixable && check.status !== "pass" && (
          <Button
            onClick={() => onFix(check.id)}
            disabled={fixing}
            className="flex items-center gap-1.5 px-3 py-1 text-xs bg-blue-600/15 hover:bg-blue-600/30 border border-blue-500/40 text-blue-200 rounded-md transition-colors disabled:opacity-50"
            title={check.fix_command}
          >
            <Wrench size={12} />
            {fixing ? "Fixing…" : "Fix"}
          </Button>
        )}
        {hasDetail && (
          <button
            type="button"
            onClick={() => setOpen((o) => !o)}
            className="p-1 text-panel-muted hover:text-panel-text"
            aria-label={open ? "Hide details" : "Show details"}
          >
            {open ? <ChevronUp size={14} /> : <ChevronDown size={14} />}
          </button>
        )}
      </div>
      {open && hasDetail && (
        <div className="px-4 pb-3 pl-11 space-y-2 text-xs">
          {check.symptom && (
            <div>
              <div className="font-semibold text-panel-text mb-0.5">What you're seeing</div>
              <div className="text-panel-muted leading-relaxed">{check.symptom}</div>
            </div>
          )}
          {check.detail && (
            <div>
              <div className="font-semibold text-panel-text mb-0.5">Detail</div>
              <pre className="bg-panel-bg/60 border border-panel-border rounded p-2 text-[11px] text-panel-muted whitespace-pre-wrap overflow-x-auto max-h-40">
                {check.detail}
              </pre>
            </div>
          )}
          {check.resolution && check.resolution.length > 0 && (
            <div>
              <div className="font-semibold text-panel-text mb-0.5">How to resolve</div>
              <ol className="list-decimal list-inside space-y-1 text-panel-muted leading-relaxed">
                {check.resolution.map((step, i) => (
                  <li key={i}>{step}</li>
                ))}
              </ol>
            </div>
          )}
          {check.fix_command && (
            <div>
              <div className="font-semibold text-panel-text mb-0.5">Command</div>
              <pre className="bg-panel-bg/60 border border-panel-border rounded p-2 text-[11px] font-mono text-panel-text overflow-x-auto">
                {check.fix_command}
              </pre>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// Static knowledge-base entries — symptoms an operator types into
// support tickets ("emails not sending") + the diagnostic check most
// likely to catch them. Helps an operator who lands on this page from
// a customer complaint find the right red row above quickly.
const KB: { symptom: string; check: string; manual: string }[] = [
  {
    symptom: "Roundcube webmail shows 'Connection to storage server failed — Could not connect to localhost:143: Connection refused'",
    check: "svc.dovecot + port.143",
    manual:
      "Dovecot daemon is down. Click Fix on either of those rows above to run `systemctl start dovecot`. If start fails, the journal tail in the row tells you why (cert missing, config parse error, port already bound).",
  },
  {
    symptom: "Inbound mail to your domains bounces with 'unknown user' even though the mailbox exists in the panel",
    check: "postfix.maps",
    manual:
      "Postfix's compiled map (.db) is out of sync with the source. Click Fix on the postfix.maps row above — runs `postmap /etc/postfix/virtual_mailbox_maps && systemctl reload postfix`.",
  },
  {
    symptom: "Outbound mail bounces with 'connection refused' on port 25 / 587",
    check: "svc.postfix + port.25 / port.587",
    manual:
      "Postfix is down or not bound. Click Fix on the svc.postfix row to start it. Verify with `postfix check` (run on the server) — any syntax error blocks startup.",
  },
  {
    symptom: "Outbound mail arrives in Gmail / Outlook with 'dkim=none' and lands in spam",
    check: "svc.opendkim",
    manual:
      "OpenDKIM is down — outbound mail leaves unsigned. Click Fix on the svc.opendkim row. Verify with `journalctl -u opendkim -f` shows 'DKIM-Signature field added' on the next outbound message.",
  },
  {
    symptom: "Bulk Upload Mailboxes returns 'failed to hash password' for every row",
    check: "doveadm.pw",
    manual:
      "doveadm CLI is missing or not on the panel's PATH. Reinstall with `apt-get install --reinstall dovecot-core` OR add `Environment=PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin` to /etc/systemd/system/serverpanel.service then `systemctl daemon-reload && systemctl restart serverpanel`.",
  },
  {
    symptom: "Specific mailboxes fail IMAP login ('Login failed') even though they appear in the panel and webmail SSO works",
    check: "users.dovecot",
    manual:
      "/etc/dovecot/users has a malformed line (often a password containing `:` from a botched bulk upload). Open the file at the line numbers shown in the users.dovecot row and delete the bad row, then re-create the mailbox via the panel.",
  },
  {
    symptom: "Apple Mail / Outlook 'Use SSL' connections fail; non-SSL connections work",
    check: "port.993 + svc.dovecot",
    manual:
      "Dovecot SSL listener is down — usually a renewed Let's Encrypt cert moved + Dovecot wasn't reloaded. Click Fix on the svc.dovecot row to restart it.",
  },
];

export default function MailIssuesPage() {
  const [report, setReport] = useState<DiagnosticReport | null>(null);
  const [loading, setLoading] = useState(false);
  const [fixingIds, setFixingIds] = useState<Set<string>>(new Set());
  const [autoFixingAll, setAutoFixingAll] = useState(false);

  const fetchReport = async () => {
    setLoading(true);
    try {
      const res = await api.get("/diagnostics/mail-stack");
      setReport(res.data?.data || null);
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to fetch diagnostic");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchReport();
  }, []);

  const fixIds = async (ids: string[], label: string) => {
    if (ids.length === 0) return;
    setFixingIds((prev) => {
      const next = new Set(prev);
      ids.forEach((id) => next.add(id));
      return next;
    });
    try {
      const res = await api.post("/diagnostics/mail-stack/fix", { ids });
      const results: FixResult[] = res.data?.data?.results || [];
      const ok = results.filter((r) => r.success).length;
      const failed = results.length - ok;
      if (failed === 0) {
        toast.success(`${label}: ${ok} of ${results.length} fixed`);
      } else {
        toast.error(
          `${label}: ${ok} fixed, ${failed} failed. First error: ${
            results.find((r) => !r.success)?.output?.slice(0, 120) || "(no detail)"
          }`,
          { duration: 8000 }
        );
      }
      await fetchReport();
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Fix call failed");
    } finally {
      setFixingIds((prev) => {
        const next = new Set(prev);
        ids.forEach((id) => next.delete(id));
        return next;
      });
    }
  };

  const fixOne = (id: string) => fixIds([id], "Fix");
  const fixAllSafe = async () => {
    if (!report) return;
    const ids = report.checks
      .filter((c) => c.status !== "pass" && c.auto_fixable)
      .map((c) => c.id);
    if (ids.length === 0) {
      toast("Nothing to auto-fix — every failing check needs manual attention.");
      return;
    }
    setAutoFixingAll(true);
    await fixIds(ids, "Auto-fix all");
    setAutoFixingAll(false);
  };

  const groupedChecks = report
    ? report.checks.reduce<Record<string, Check[]>>((acc, c) => {
        (acc[c.group] ||= []).push(c);
        return acc;
      }, {})
    : {};

  const safeFixCount = report
    ? report.checks.filter((c) => c.status !== "pass" && c.auto_fixable).length
    : 0;

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between gap-3 flex-wrap">
        <div>
          <h1 className="text-xl font-bold text-panel-text flex items-center gap-2">
            <Mail size={20} className="text-blue-400" />
            Mail Issues &amp; Resolution
          </h1>
          <p className="text-panel-muted text-sm mt-1">
            Diagnostic for Dovecot + Postfix + OpenDKIM. Each red / amber row carries the symptom an
            operator sees in the panel + the step-by-step resolution; safe fixes can be applied with one click.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            onClick={fetchReport}
            disabled={loading}
            className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text text-sm disabled:opacity-50"
          >
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
            {loading ? "Refreshing…" : "Re-run diagnostic"}
          </Button>
          {safeFixCount > 0 && (
            <Button
              onClick={fixAllSafe}
              disabled={autoFixingAll}
              className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium disabled:opacity-50"
              title={`Run safe auto-fix on ${safeFixCount} check(s)`}
            >
              <Wrench size={14} />
              {autoFixingAll ? "Fixing…" : `Auto-fix ${safeFixCount} safe issue${safeFixCount === 1 ? "" : "s"}`}
            </Button>
          )}
        </div>
      </div>

      {/* Summary */}
      {report && (
        <div className="grid grid-cols-3 gap-3">
          <SummaryTile label="Healthy" count={report.summary.pass} tone="emerald" icon={<CheckCircle2 size={20} />} />
          <SummaryTile label="Warnings" count={report.summary.warn} tone="amber" icon={<AlertTriangle size={20} />} />
          <SummaryTile label="Failures" count={report.summary.fail} tone="red" icon={<XCircle size={20} />} />
        </div>
      )}

      {/* Per-group cards */}
      {report &&
        Object.keys(groupedChecks).map((group) => {
          const checks = groupedChecks[group];
          const Icon = GROUP_ICONS[group] || Settings;
          const failCount = checks.filter((c) => c.status !== "pass").length;
          return (
            <Card key={group}>
              <div className="px-4 py-3 border-b border-panel-border flex items-center gap-2">
                <Icon size={14} className="text-panel-muted" />
                <h2 className="text-sm font-semibold text-panel-text">{group}</h2>
                <span className="text-xs text-panel-muted ml-auto">
                  {failCount === 0
                    ? `${checks.length} check${checks.length === 1 ? "" : "s"} — all healthy`
                    : `${failCount} of ${checks.length} need attention`}
                </span>
              </div>
              <div>
                {checks.map((c) => (
                  <CheckRow
                    key={c.id}
                    check={c}
                    fixing={fixingIds.has(c.id)}
                    onFix={fixOne}
                  />
                ))}
              </div>
            </Card>
          );
        })}

      {/* Knowledge-base */}
      <Card>
        <div className="px-4 py-3 border-b border-panel-border">
          <h2 className="text-sm font-semibold text-panel-text">Common Symptoms → Resolution</h2>
          <p className="text-xs text-panel-muted mt-0.5">
            Customer-visible symptoms an operator hears about, mapped to the diagnostic check above + the
            playbook to follow when "Auto-fix" can't reach it.
          </p>
        </div>
        <div className="divide-y divide-panel-border">
          {KB.map((entry) => (
            <div key={entry.symptom} className="px-4 py-3 space-y-1">
              <div className="text-sm text-panel-text font-medium">{entry.symptom}</div>
              <div className="text-xs text-panel-muted">
                Check rows: <span className="font-mono text-blue-300">{entry.check}</span>
              </div>
              <div className="text-xs text-panel-muted leading-relaxed">{entry.manual}</div>
            </div>
          ))}
        </div>
      </Card>

      {report && (
        <p className="text-[11px] text-panel-muted text-center">
          Diagnostic generated at {new Date(report.generated_at).toLocaleString()}
        </p>
      )}
    </div>
  );
}

function SummaryTile({
  label,
  count,
  tone,
  icon,
}: {
  label: string;
  count: number;
  tone: "emerald" | "amber" | "red";
  icon: React.ReactNode;
}) {
  const cls =
    tone === "emerald"
      ? "border-emerald-500/30 bg-emerald-500/5 text-emerald-300"
      : tone === "amber"
      ? "border-amber-500/30 bg-amber-500/5 text-amber-300"
      : "border-red-500/30 bg-red-500/5 text-red-300";
  return (
    <div className={`rounded-lg border ${cls} p-4 flex items-center gap-3`}>
      {icon}
      <div>
        <div className="text-2xl font-bold leading-none">{count}</div>
        <div className="text-xs uppercase tracking-wide mt-1 opacity-80">{label}</div>
      </div>
    </div>
  );
}
