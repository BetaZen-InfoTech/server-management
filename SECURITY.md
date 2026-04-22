# Security Policy

BetaZen InfoTech takes the security of Betazen Server Panel very seriously. The panel sits at the root of its operators' hosting infrastructure — a serious vulnerability affects not just the operator, but every tenant and end-customer they serve. This document describes how to report, what we promise in return, and what is in/out of scope.

---

## 1. Supported versions

Security fixes are issued only for the following branches:

| Branch / version | Supported | Notes |
|---|---|---|
| `main` (rolling) | ✅ Yes | Active development; fixes land here first. |
| Latest tagged minor (`1.x` where `x` is the highest minor) | ✅ Yes | Fixes backported as patch releases. |
| Previous minor (one release behind latest) | ⚠️ Critical fixes only | No feature or dependency updates. |
| Anything older | ❌ No | Please upgrade first — see [`docs/server-panel-upgrade.md`](./docs/server-panel-upgrade.md). |

The version you are running is visible at `GET /api/v1/version` and in the WHM top bar.

---

## 2. Reporting a vulnerability

**Do NOT open a public GitHub issue, discussion, pull request, or tweet about suspected vulnerabilities.** Public disclosure before a fix is shipped puts every self-hosted operator at risk.

### Preferred channel

Email **security@betazeninfotech.com** with as much of the following as you can provide:

- A short description of the issue and its impact.
- The affected endpoint(s), file(s), or feature(s).
- A reproduction: steps, HTTP requests, a curl/PoC script, or a minimal patch.
- The version you tested against (`GET /api/v1/version` output is perfect).
- Your name/handle if you want credit in the advisory, or "anonymous" if not.

If the disclosure is sensitive, you may encrypt your email with our PGP key (fingerprint will be published at `https://betazeninfotech.com/.well-known/security.txt` once that service is live — in the interim, request the public key in an initial unencrypted email and we will reply with it).

### Alternate channels

- **GitHub private security advisory** — open one at `https://github.com/BetaZen-InfoTech/server-management/security/advisories/new`. This routes directly to our security team without being public.

Please do **not** use `support@` for vulnerability reports — that queue is not cleared to see pre-disclosure material.

---

## 3. What we commit to

Once a valid report is received, BetaZen InfoTech will:

| Stage | Target response time |
|---|---|
| Acknowledge receipt of your report | within **72 hours** |
| Provide a preliminary severity assessment | within **7 days** |
| Keep you updated on remediation progress | at least every **14 days** until resolution |
| Ship a coordinated fix | typically within **30 days** for High/Critical, **90 days** for Medium, best-effort for Low |
| Publish a post-fix advisory | within **14 days** of the patched release |

Severities use a CVSS-v3.1-inspired rubric: Critical, High, Medium, Low, Informational.

### Coordinated disclosure

We follow a **90-day coordinated-disclosure window** by default, counting from the date of our first acknowledgement. If the fix is shipped earlier, we will disclose sooner. If the vulnerability is actively being exploited in the wild, we will accelerate. We will never retaliate against, or pursue legal action against, a researcher acting in good faith under this policy.

### Credit / "Hall of Fame"

With your permission, we will credit you in the published advisory and in a security acknowledgements page on `betazeninfotech.com`. You can choose your handle, a real name, or anonymity.

### Bug bounty

At this time we do not operate a monetary bug-bounty program. We are happy to send BetaZen swag and to prominently credit researchers who report Medium-or-higher issues. This policy may change as the project grows — check back.

---

## 4. Scope

### In scope

All code hosted at `https://github.com/BetaZen-InfoTech/server-management`, including:

- The Go backend (`backend/`).
- The VPS agent daemon (`backend/cmd/agent`).
- Both frontend SPAs (`frontend/apps/whm` and `frontend/apps/cpanel`).
- The installer (`install.sh`) and helper scripts (`scripts/`).
- Official Docker images, CI workflows, and deployment artefacts.
- Any asset reachable from `https://panel.betazeninfotech.com` or other hosts under `betazeninfotech.com` that we operate.

Example issues we want to hear about:

- Authentication / session / JWT bugs, token-leakage, privilege escalation.
- Cross-tenant data access (any caller from tenant A being able to read/write data of tenant B).
- RBAC bypasses — a role being able to call an endpoint it should not.
- SQL / NoSQL injection, command injection, path traversal, SSRF.
- mTLS / agent-channel weaknesses.
- XSS, CSRF, open redirects, clickjacking, HTML injection with impact.
- Supply-chain or build-time risks (installer fetching compromised artefacts, CI secrets exposed).
- Any vulnerability that results in loss of integrity, confidentiality, or availability of panel or tenant data.

### Out of scope

The following are **not** considered vulnerabilities under this policy. Please do not report them:

- Self-XSS that requires the victim to paste attacker-controlled code into their own browser console.
- Automated scanner findings ("missing X-Frame-Options", "TLS 1.0 enabled", etc.) without a demonstrable security impact on a supported, correctly-configured install.
- Best-practice / theoretical reports without a reproduction (e.g. "your JWT lifetime is longer than I'd like").
- Clickjacking on endpoints with no sensitive state-changing action.
- Email spoofing due to DMARC policy on the operator's own mail domains — this is the operator's configuration, not the panel's.
- Denial-of-service that only works with authenticated admin-level access.
- Rate-limiting gaps on non-authenticated endpoints where no state-changing side effect is possible.
- Third-party, system-level services (MongoDB, MariaDB, Postfix, Dovecot, nginx, …) installed by `install.sh` — report those upstream. We will of course ship configuration hardening if the vulnerability stems from our defaults.
- Findings from an install with default seeded credentials still in place. The installer prints a giant warning on first launch to change `admin@betazeninfotech.com / admin123`; not doing so is the operator's mistake.

### Testing rules

You **must**:

- Test only on instances you own or are explicitly authorized to test.
- Use test accounts — never attempt to access real customer data.
- Stop testing immediately and report if you inadvertently access another tenant's data.
- Avoid any destructive testing (deleting data, dropping databases, spamming real mailboxes).
- Not pivot from a vulnerability in the panel to other infrastructure, third-party services, or the operator's customers.

Violating these rules voids the safe-harbour protection in Section 5.

---

## 5. Safe harbour

If you follow this policy in good faith, BetaZen InfoTech will:

- Consider your research authorized access to our systems and code.
- Not pursue civil litigation or ask law enforcement to investigate your report.
- Not treat your research as a breach of our Terms of Service or this LICENSE, provided you do not retain, modify, or exfiltrate data beyond what is strictly necessary to demonstrate the issue.

This safe-harbour protection applies only to the extent permitted by Indian law and does not extend to activity that would be criminal regardless of authorization (for example, identity theft, extortion, or attacks on third-party systems).

---

## 6. Security hardening recommendations (for operators)

If you self-host Betazen Server Panel, please do at minimum:

1. **Change the seeded admin password immediately** after first login.
2. **Rotate `JWT_SECRET` and `APP_ENCRYPTION_KEY`** in `/opt/serverpanel/.env` from the installer defaults (the installer randomizes these, but verify on your install).
3. **Restrict SSH** to key-based auth, on a non-standard port, behind ufw allowlists.
4. **Lock the agent port (8443)** to the panel-server IP only; the installer does this via `ufw` but verify.
5. **Enable HTTPS from the panel** as soon as DNS points at the server — go to **SSL** and issue a Let's Encrypt cert.
6. **Subscribe to advisories** by watching the [GitHub repository](https://github.com/BetaZen-InfoTech/server-management) (Releases only, at minimum).
7. **Run `serverpanel` only under its dedicated user**; do not run as root.

---

## 7. Contact

Security reports .................. security@betazeninfotech.com
Legal notices ..................... legal@betazeninfotech.com
Code of Conduct enforcement ....... conduct@betazeninfotech.com

BetaZen InfoTech
Kolkata, West Bengal, India
https://betazeninfotech.com
