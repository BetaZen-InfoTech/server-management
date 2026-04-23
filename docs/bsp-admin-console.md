# Betazen Server Panel — Admin Console (`bsp`)

The `bsp` command is a root-only, SSH-facing interactive admin console for the Betazen Server Panel. Type `bsp` on the server after logging in as root and you'll land on a numbered menu that can rotate the super admin email, rotate the super admin password, change the panel access domain + issue SSL, renew SSL, show panel info, and restart the service.

It's a thin alias for the same binary (`/opt/serverpanel/bin/bzpanel`), so every menu action maps one-to-one to the existing `bzpanel <subcommand>` form. Use `bsp` when you want a friendly menu; use `bzpanel <subcommand>` when you're scripting.

---

## Table of contents

1. [Quick start](#1-quick-start)
2. [What's on the menu](#2-whats-on-the-menu)
3. [Scripted equivalents (`bzpanel`)](#3-scripted-equivalents-bzpanel)
4. [Files it touches](#4-files-it-touches)
5. [Security model](#5-security-model)
6. [Troubleshooting](#6-troubleshooting)

---

## 1. Quick start

```bash
ssh root@your.server.tld
bsp
```

You'll see something like:

```
═══════════════════════════════════════════════════════════
  Betazen Server Panel — Admin Console  (3.0.0)
───────────────────────────────────────────────────────────
  Panel URL : https://panel.example.com
  Server IP : 198.51.100.42
  Admin     : admin@example.com
  Service   : active
═══════════════════════════════════════════════════════════

  1) Update root admin email
  2) Update root admin password
  3) Set/update panel URL + activate SSL
  4) Renew / update SSL
  5) Customer support (coming soon)
  6) Show panel info
  7) Restart panel service
  0) Exit

Select [0-7]:
```

Pick a number, follow the prompts. After each action the console asks **"Return to menu? [Y/n]"** so you can chain several changes without re-running the binary.

`bsp` only starts the interactive menu when stdin is a real terminal. Piping (`echo 1 | bsp`) falls through to the old-style usage output so non-interactive scripts don't hang waiting for input.

---

## 2. What's on the menu

| # | Action | What it does |
|---|---|---|
| 1 | **Update root admin email** | Prompts for a new email, validates format, enforces the global email-uniqueness rule, updates the `vendor_owner` user doc in Mongo. |
| 2 | **Update root admin password** | Prompts twice (no echo), bcrypt-hashes, writes to Mongo, clears refresh and reset tokens so stale sessions are invalidated. |
| 3 | **Set/update panel URL + activate SSL** | Asks for the new FQDN, rewrites `.env` (`DOMAIN`, `MAIL_HOSTNAME`), re-emits the nginx vhost in HTTP form, runs `nginx -t` + `systemctl reload nginx`, restarts `serverpanel`, then chains into SSL issuance (optional). |
| 4 | **Renew / update SSL** | Runs `certbot certonly --webroot` against the current domain, rewrites the vhost in HTTPS form, reloads nginx. Idempotent — certbot no-ops when the existing cert is still fresh. |
| 5 | **Customer support** | Placeholder. For now the prompt points to `support@betazeninfotech.com`. |
| 6 | **Show panel info** | Prints product version, domain, server IP, current super admin, detected SSL (http vs https), and systemd state. Read-only. |
| 7 | **Restart panel service** | `systemctl restart serverpanel`. Useful after an env-var change that bypassed option 3. |
| 0 | **Exit** | Returns to the shell. `q`, `quit`, `exit`, and an empty line also exit. |

Failures during any action are printed to stderr and the menu redraws — a typo in an email doesn't kick you back to the shell.

---

## 3. Scripted equivalents (`bzpanel`)

Every menu choice has a scripted counterpart under the `bzpanel` binary (same binary, different invocation). Use these in CI, in your own admin scripts, or when you want a one-shot rotation without the menu loop.

```bash
# Menu option 1 — rotate super admin email
bzpanel admin-email new-admin@example.com

# Menu option 2 — rotate super admin password (prompts if omitted)
bzpanel admin-password
bzpanel admin-password 'N3wSecret!'

# Menu option 3 (first half) — change panel domain
bzpanel domain panel.example.com

# Menu option 3 (second half) / menu option 4 — issue or renew SSL
bzpanel ssl
bzpanel ssl --email admin@example.com

# Menu option 6 — show info
bzpanel info

# Menu option 7 — restart the panel
bzpanel restart
bzpanel status
```

The interactive menu calls the same `cmd*` functions these subcommands call, so behavior stays in lockstep between the two forms.

---

## 4. Files it touches

| File | When | Why |
|---|---|---|
| `/opt/serverpanel/.env` | Option 3 | Rewrites `DOMAIN=` and `MAIL_HOSTNAME=` in-place (atomic `.tmp` + `rename`). |
| `/etc/nginx/sites-available/serverpanel` | Options 3, 4 | Re-emits the vhost (HTTP form after a domain change, HTTPS form after SSL issuance). Managed by `bsp` — don't hand-edit. |
| `/etc/nginx/sites-enabled/serverpanel` | Option 4 | Re-creates the symlink if missing. |
| `/etc/letsencrypt/live/<domain>/*.pem` | Option 4 | Issued / renewed by `certbot certonly --webroot -w /var/www/certbot`. |
| MongoDB `users` collection | Options 1, 2 | Updates the `vendor_owner` document. |

`bsp` never writes outside those paths, so the impact of a misinvocation is contained.

---

## 5. Security model

- **Root-only** — reads Mongo via the panel's own credentials from `/opt/serverpanel/.env`, which is owned by root (mode `0600`). Running `bsp` as any other user can't read that file and the binary fails cleanly.
- **Password echo off** — option 2 uses `golang.org/x/term.ReadPassword` so the new password never appears in the terminal or in scrollback.
- **Stale sessions invalidated** — rotating the password clears `refresh_token`, `refresh_expires_at`, `reset_token_hash`, `reset_expires_at`, and `failed_logins` so an attacker who's already logged in gets bounced on the next access-token refresh.
- **Email uniqueness** — option 1 enforces the same global case-insensitive uniqueness the public API does, so it can't create a collision that would break login.
- **Nginx verified before reload** — options 3 and 4 run `nginx -t` and abort if the config is invalid, so a typo in your domain name can't take the public proxy down.

---

## 6. Troubleshooting

**`bsp: command not found`** — the binary was installed but the symlink is missing. Restore it:

```bash
ln -sf /opt/serverpanel/bin/bzpanel /usr/local/bin/bsp
ln -sf /opt/serverpanel/bin/bzpanel /usr/local/bin/bzpanel
```

If `/opt/serverpanel/bin/bzpanel` itself is missing, rebuild it:

```bash
cd /opt/serverpanel
/opt/go/1.23/bin/go -C backend build -o /opt/serverpanel/bin/bzpanel ./cmd/bzpanel
```

**"no vendor_owner in … .users"** — the database hasn't been seeded yet. Run the installer's seed step (or `./bin/seed`) before using options 1, 2, or 6.

**`nginx -t` fails after option 3** — revert by pointing `DOMAIN` back to the previous FQDN and re-running option 3, or restore `/etc/nginx/sites-available/serverpanel` from backup. The panel systemd unit keeps running on the old config until nginx is reloaded.

**`certbot` fails in option 4** — the most common cause is DNS not yet pointing to the server. Wait for propagation and re-run option 4. Rate-limited? Try the Let's Encrypt staging environment by invoking `certbot` directly (not through `bsp`) with `--staging`.

**Menu won't show / exits immediately** — `bsp` only starts the menu when stdin is a TTY. If you're inside `tmux`, `screen`, or a normal SSH session it'll work. If you ran it through a pipe (e.g. via Ansible's `command` module), switch to the scripted `bzpanel <subcommand>` form instead.
