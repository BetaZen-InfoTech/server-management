# Contributing to Betazen Server Panel

First of all — thank you for considering a contribution. Betazen Server Panel is maintained by **BetaZen InfoTech** as a source-available project, and every high-quality patch, bug report, or docs fix makes the product better for every self-hosting operator in the community.

Please read this document in full **before** opening an issue or pull request.

---

## Table of contents

1. [Code of conduct](#1-code-of-conduct)
2. [Ground rules (read this first)](#2-ground-rules-read-this-first)
3. [Contributor License Agreement (CLA)](#3-contributor-license-agreement-cla)
4. [Reporting bugs](#4-reporting-bugs)
5. [Requesting features](#5-requesting-features)
6. [Reporting security issues](#6-reporting-security-issues)
7. [Development setup](#7-development-setup)
8. [Branching, commits, and PRs](#8-branching-commits-and-prs)
9. [Coding standards](#9-coding-standards)
10. [Testing requirements](#10-testing-requirements)
11. [Documentation](#11-documentation)
12. [Review process and SLA](#12-review-process-and-sla)
13. [License of your contribution](#13-license-of-your-contribution)

---

## 1. Code of conduct

This project follows the [Contributor Covenant Code of Conduct](./CODE_OF_CONDUCT.md). By participating you agree to uphold it. Enforcement contact: **conduct@betazeninfotech.com**.

---

## 2. Ground rules (read this first)

- **This is source-available, not OSI-approved open source.** Contributions are welcome, but they are governed by the [BetaZen InfoTech Source-Available License](./LICENSE). In particular, Section 3 prohibits any derivative use that would constitute a *Competing Service*. Do not submit a contribution whose primary purpose is to defeat that restriction.
- **No trademark-stripping forks.** Patches that remove, replace, or rename BetaZen / Betazen Server Panel branding will be closed without merge. Private forks that do this for internal use are covered by Section 7.4 of the LICENSE; we just won't upstream them.
- **Stay in scope.** This project is a hosting control panel. It is not a generic "workflow", "DevOps", or "AI agent" framework. Feature requests outside that scope will be politely declined.
- **One concern per PR.** Bundling an unrelated refactor with a bugfix makes review slower and the `git blame` noisier. Open two PRs.
- **Keep `main` deployable.** The repository's CI and `install.sh` must keep working on a *fresh Ubuntu 22.04/24.04 VPS* after every merge. If your patch touches `install.sh`, please test it on a throwaway VPS before requesting review.

---

## 3. Contributor License Agreement (CLA)

By submitting a pull request to this repository you represent and agree that:

1. The contribution is **your original work**, or you have sufficient rights from your employer / original author to submit it.
2. You grant BetaZen InfoTech a **perpetual, worldwide, royalty-free, non-exclusive, irrevocable license** to use, reproduce, modify, distribute, sub-license, and re-license your contribution as part of the Software — including the right to re-license it under a future commercial license and under the AGPL-3.0 Change License referenced in Section 4 of the LICENSE.
3. You retain copyright in your contribution. This CLA is a *license to*, not an *assignment of*, your copyright.
4. Your contribution is provided **"AS IS"** without warranty of any kind, consistent with Section 6 of the LICENSE.

For small patches (< ~100 lines of non-trivial code, or docs-only) the CLA is asserted by the act of opening the pull request. For larger contributions, or contributions on behalf of a company, we may ask you to countersign a short CLA document out-of-band — a maintainer will ping you on the PR and send the CLA to the email on your GitHub account. We try to keep the bar as low as legally prudent.

---

## 4. Reporting bugs

Before opening an issue:

1. Search [existing issues](https://github.com/BetaZen-InfoTech/server-management/issues?q=is%3Aissue) — odds are someone already reported it.
2. Make sure you are on a recent `main`. Re-run `curl -sSL .../install.sh | bash` to pull latest; the installer is idempotent.
3. Collect:
   - `serverpanel --version` (or `GET /api/v1/version`)
   - Ubuntu release (`lsb_release -ds`)
   - `journalctl -u serverpanel -n 200 --no-pager`
   - MongoDB version (`mongod --version`)
   - Exact reproduction steps

Open the issue with the **Bug report** template. Do **not** paste `.env`, JWT secrets, mTLS private keys, or customer data into the issue — redact first.

---

## 5. Requesting features

Feature requests are welcome via GitHub Issues using the **Feature request** template. Please include:

- The operator pain point (who is blocked, and how often).
- How cPanel / Plesk / CyberPanel / DirectAdmin solves the same problem today (if they do).
- Whether you are willing to submit a PR for it.

We will close requests that are purely aesthetic, or that fall outside the "hosting control panel" scope defined above.

---

## 6. Reporting security issues

**Do NOT open a public GitHub issue for a security vulnerability.** See [SECURITY.md](./SECURITY.md) for the coordinated-disclosure process. TL;DR: email **security@betazeninfotech.com** with reproduction details; we respond within 72 hours.

---

## 7. Development setup

Prerequisites on your dev machine:

| Requirement | Version |
|---|---|
| Go | 1.22+ |
| Node.js | 18 LTS, 20 LTS, or 22 LTS |
| npm | 10+ |
| MongoDB | 8.0+ (local or Atlas) |
| make | GNU Make 4+ |

```bash
git clone https://github.com/BetaZen-InfoTech/server-management.git
cd server-management

cp .env.example .env          # fill in MONGO_URI + JWT_SECRET
make setup                    # go mod download + npm install
make dev                      # spawns backend (Air) + WHM + User Panel SPAs
```

The dev servers:

- Backend API — `http://localhost:8080`
- WHM SPA — `http://localhost:5173`
- User Panel SPA — `http://localhost:5174`

The WHM and User Panel Vite dev servers proxy `/api/v1/*` to the backend automatically.

---

## 8. Branching, commits, and PRs

- **Never commit to `main` directly** — always branch. Suggested naming: `fix/<short-slug>`, `feat/<short-slug>`, `docs/<short-slug>`, `chore/<short-slug>`.
- Base your branch on the latest `origin/main`. Rebase (don't merge) `main` into your branch to keep history linear.
- **Commit message style** — imperative mood, scoped:
  ```
  scope: short summary in imperative mood

  Optional body that explains the WHY, not the WHAT.
  Link to the issue number (e.g., Closes #123) if applicable.
  ```
  Good scopes: `whm`, `user-panel`, `backend`, `agent`, `install`, `docs`, `ci`, `deps`, `mail`, `dns`, `transfer`.
- **Keep commits small.** We do not squash on merge by default — your commits will appear in `git log` verbatim, so write them like you'll read them in six months.
- **Sign-off is not required,** but a DCO-style `Signed-off-by:` line is welcome for contributions from employees of companies with strict IP policies.
- **Open the PR against `main`.** Fill in the PR template: what, why, screenshots for any UI change, and a manual-test checklist.

---

## 9. Coding standards

### Go backend

- Target **Go 1.22**. Do not use features from later versions without updating `go.mod` and CI.
- Follow standard `gofmt` / `goimports`. CI runs `golangci-lint`. Run `make lint` before pushing.
- Handler → Service → Database separation is strict. Handlers may only call services; services own all DB + side effects.
- All API responses go through `pkg/response` helpers so the shape stays consistent.
- Auth on new endpoints: wire RBAC at the route level (`middleware.RequirePerm(...)`), not inside the handler body.
- Token fields in JSON are **snake_case** (`access_token`, `refresh_token`). Don't "fix" this — see CLAUDE.md for the history.

### Frontend (React + TypeScript)

- TypeScript strict mode is on. No `any` in new code; prefer proper types from `packages/types`.
- API calls go through `packages/api-client` — do not call `axios` directly from components.
- State that needs to survive route changes lives in Zustand stores under `apps/<app>/src/stores/`.
- Tailwind first; only drop into plain CSS for genuine exceptions.
- UI components that are shared between WHM and the User Panel live in `packages/ui`.

### Install / shell scripts

- `install.sh` must stay idempotent — re-running it on an existing install must not break state, must not drop user data, and must not restart services unless something actually changed.
- Use `set -euo pipefail` in any new shell file. Log with the same `log_*` helpers `install.sh` already exports.

---

## 10. Testing requirements

- **Backend:** `go test ./...` must pass. For new service code, add at least one unit test covering the happy path and one for the most realistic failure mode.
- **Frontend:** `turbo test` must pass. We use Vitest; place tests next to the code as `*.test.ts` / `*.test.tsx`.
- **Integration:** UI changes that touch auth, billing, or the transfer wizard must be manually tested end-to-end on a test VPS. Describe the manual-test steps in the PR body — a reviewer will re-run them.
- **CI** (`.github/workflows/*`) runs `make lint && make test` on every PR. A red CI is a blocker; don't `--no-verify` your way past it — fix the underlying issue.

---

## 11. Documentation

- If you change user-facing behavior, update the relevant doc in the same PR: [`README.md`](./README.md), [`DEPLOYMENT.md`](./DEPLOYMENT.md), [`FEATURES_VENDOR_WHM.md`](./FEATURES_VENDOR_WHM.md), or the relevant file under [`docs/`](./docs/).
- If you add an API endpoint, add it to the API table in `README.md` and ensure the response shape matches the `{ success, data, error, pagination }` contract.
- Screenshots for UI changes go in `docs/screenshots/` (create the directory if needed). Keep them under 300 KB each.

---

## 12. Review process and SLA

- One maintainer approval is required to merge. For changes that touch auth, billing, or the mTLS channel, **two** maintainer approvals are required.
- Maintainers aim to triage new issues within **3 business days** and provide first PR feedback within **5 business days**. We are a small team — if a PR goes quiet, it is fine to ping the thread after a week.
- PRs that are inactive for 30 days will be marked `stale` and closed after another 14 days. Re-open any time.

---

## 13. License of your contribution

By opening a pull request you acknowledge and agree that:

1. Your contribution is licensed to BetaZen InfoTech under the terms described in Section 3 of this document (the CLA).
2. Your contribution will become part of the Software and will be distributed under the [BetaZen InfoTech Source-Available License](./LICENSE), including the Change-Date conversion to AGPL-3.0-or-later described in Section 4 of that license.
3. You will not submit contributions that you know to be encumbered by third-party copyrights, patents, or NDAs that are incompatible with the LICENSE.

Thanks again for contributing. See you in the PR queue.

— The Betazen Server Panel maintainers
  BetaZen InfoTech · Kolkata, India
