import React, { useState } from "react";
import { Eye, EyeOff, KeyRound } from "lucide-react";

// generatePassword returns a 16-character cryptographically random
// password drawn from a 70-char alphabet that mixes upper, lower,
// digits, and a small set of shell-friendly symbols. The symbol pool
// excludes characters that commonly trip shell quoting (`'`, `"`,
// `\`, `` ` ``) so the generated value pastes cleanly into terminals,
// .env files, and HTTP Basic Auth headers without escape headaches.
//
// Uses crypto.getRandomValues — Math.random would be a downgrade. Both
// browsers and Node 19+ ship the WebCrypto API, so the helper is safe
// to import from any place rendered in this monorepo.
export function generatePassword(length = 16): string {
  const alphabet =
    "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#$%^&*";
  const arr = new Uint32Array(length);
  const c =
    typeof crypto !== "undefined"
      ? crypto
      : (window as unknown as { crypto: Crypto }).crypto;
  c.getRandomValues(arr);
  return Array.from(arr, (n) => alphabet[n % alphabet.length]).join("");
}

interface PasswordInputProps
  extends Omit<
    React.InputHTMLAttributes<HTMLInputElement>,
    "type" | "onChange" | "value"
  > {
  value: string;
  // String-shaped onChange — most password fields update string state, so
  // the parent's setter is a one-liner: `(v) => setForm({...form, pw: v})`.
  onChange: (value: string) => void;
  // Tailwind classes for the inner <input>. Defaults are sensible but
  // every page in this repo brings its own `inputClass` so we accept it.
  inputClassName?: string;
  // Extra classes for the outer flex wrapper (rarely needed).
  wrapperClassName?: string;
  // Tailwind classes for the two side buttons. Rarely overridden.
  buttonClassName?: string;
  // When true, the password is shown in plaintext from first render.
  // Useful in "rotate password" flows where the operator wants to verify
  // what they're about to set. Default false matches the standard
  // password-field expectation.
  showInitial?: boolean;
  // Hides the eye toggle. Almost never used — included so the same
  // component can be embedded in highly compact rows.
  hideShowToggle?: boolean;
  // Hides the dice button. Use when the field actually holds an
  // EXTERNAL secret (Git PAT, SMTP relay password, source-server SSH
  // password) — generating one would be wrong because the operator
  // is typing a credential that already exists somewhere else.
  hideGenerator?: boolean;
  // Length of the generated password. Default 16.
  generateLength?: number;
}

// PasswordInput is the canonical shape for any "type a new password"
// field across both SPAs. It bundles:
//   * the input itself (type swaps between password / text on toggle)
//   * an eye / eye-off button to reveal what was typed
//   * a "dice" key-round button that fills the field with a strong
//     random password and auto-reveals it so the operator can copy it
//     before submitting
//
// Pre-3.0.23 each page reimplemented this inline. Three pages had
// generators and 22 didn't — the inconsistency was visible to operators
// and weak passwords sneaked into team-member, mailbox, and HTTP-auth
// flows because there was no nudge towards a strong default.
export function PasswordInput({
  value,
  onChange,
  inputClassName,
  wrapperClassName,
  buttonClassName,
  showInitial = false,
  hideShowToggle = false,
  hideGenerator = false,
  generateLength = 16,
  ...inputProps
}: PasswordInputProps) {
  const [shown, setShown] = useState(showInitial);

  // Default styling matches the existing password fields in the repo
  // (panel-bg / panel-border palette). Pages that brought their own
  // inputClassName win — we don't want to silently change visual style.
  const defaultInput =
    "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500";
  const defaultBtn =
    "px-3 py-2 text-xs border border-panel-border rounded-lg text-panel-muted hover:text-panel-text shrink-0";

  const handleGenerate = () => {
    onChange(generatePassword(generateLength));
    setShown(true);
  };

  return (
    <div className={`flex gap-2 ${wrapperClassName ?? ""}`}>
      <input
        type={shown ? "text" : "password"}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className={`${inputClassName ?? defaultInput} flex-1`}
        {...inputProps}
      />
      {!hideShowToggle && (
        <button
          type="button"
          onClick={() => setShown((s) => !s)}
          className={buttonClassName ?? defaultBtn}
          title={shown ? "Hide password" : "Show password"}
          aria-label={shown ? "Hide password" : "Show password"}
        >
          {shown ? <EyeOff size={14} /> : <Eye size={14} />}
        </button>
      )}
      {!hideGenerator && (
        <button
          type="button"
          onClick={handleGenerate}
          className={buttonClassName ?? defaultBtn}
          title="Generate a strong password"
          aria-label="Generate a strong password"
        >
          <KeyRound size={14} />
        </button>
      )}
    </div>
  );
}
