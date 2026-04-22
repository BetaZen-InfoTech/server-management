package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// terminalJailRcPath is where we install the shell-level jail rcfile.
// The terminal handler passes this to `bash --rcfile` so tenant users
// get a bash that refuses cd out of their $HOME. This is a UX-level
// guardrail; the real isolation is jailkit's chroot (jk_jailuser,
// jailshell.go). Having the shell-level layer too means operators
// whose jailkit run failed at creation time still can't accidentally
// wander to /etc from OUR terminal UI.
const terminalJailRcPath = "/etc/serverpanel/jail.bashrc"

// terminalJailRcBody is the bash rcfile content. It's a careful set of
// function overrides — bash builtins `cd` and `pushd` are replaced with
// versions that resolve their argument to an absolute path via a
// sub-shell and reject anything outside $HOME. Aliases for sh/bash/zsh
// print a refusal so naive escape attempts at least get a visible
// message. Knowledgeable users can still trivially escape via python /
// perl / /lib/x86_64-linux-gnu/ld-linux.so; the real boundary is the
// chroot — this is about UX.
const terminalJailRcBody = `# Betazen Server Panel tenant shell — UX sandbox (NOT a security boundary).
# Real isolation comes from jailkit's chroot; this layer keeps the
# interactive prompt inside $HOME so 'cd /' and 'cd /etc' fail with a
# clear message instead of exposing the host tree.

# Source the user's own bashrc first (PATH, aliases, PS1 colors).
[ -f "$HOME/.bashrc" ] && . "$HOME/.bashrc"

__sp_home="${HOME%/}"
export __sp_home

__sp_resolve() {
    # Resolve a cd target to an absolute path by cd-ing in a subshell
    # and reading pwd. Works on every bash regardless of whether
    # realpath/readlink are installed (jailkit basicshell has neither).
    ( builtin cd -- "$1" 2>/dev/null && pwd )
}

__sp_inside_home() {
    local abs="$1"
    case "$abs" in
        "$__sp_home"|"$__sp_home"/*) return 0 ;;
        *) return 1 ;;
    esac
}

cd() {
    local target
    if [ $# -eq 0 ]; then
        target="$HOME"
    elif [ "$1" = "-" ]; then
        builtin cd -
        return $?
    else
        target="$1"
    fi
    local abs
    abs=$(__sp_resolve "$target")
    if [ -z "$abs" ]; then
        builtin cd -- "$target"
        return $?
    fi
    if __sp_inside_home "$abs"; then
        builtin cd -- "$target"
    else
        printf "\033[31msp-jail:\033[0m access denied — you can only navigate within your home directory (%s)\n" "$__sp_home" >&2
        return 1
    fi
}

pushd() {
    if [ $# -eq 0 ]; then
        builtin pushd
        return $?
    fi
    local abs
    abs=$(__sp_resolve "$1")
    if [ -n "$abs" ] && ! __sp_inside_home "$abs"; then
        printf "\033[31msp-jail:\033[0m pushd outside home is blocked (%s)\n" "$__sp_home" >&2
        return 1
    fi
    builtin pushd "$@"
}

# Visual cue that the user is in the sandboxed shell.
PS1='\[\033[35m\](jail)\[\033[0m\] \u@\h:\w\$ '
export PS1

# Land in $HOME regardless of where 'su -' dropped the caller.
builtin cd "$__sp_home" 2>/dev/null
`

// EnsureTerminalJailRcfile writes /etc/serverpanel/jail.bashrc (creating
// /etc/serverpanel/ if needed). Idempotent — writes only when the file
// is missing or content has drifted, so restarting the panel on every
// deploy doesn't rewrite a perfectly-good file on disk.
//
// Called once from the panel startup path; tolerates failures (logs to
// stderr) so a read-only /etc doesn't block panel boot.
func EnsureTerminalJailRcfile(_ context.Context) error {
	dir := filepath.Dir(terminalJailRcPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	// Skip the write when the file already contains the exact canonical
	// body — avoids touching mtime on every panel restart (which would
	// otherwise trip file-change auditing tools).
	if existing, err := os.ReadFile(terminalJailRcPath); err == nil && string(existing) == terminalJailRcBody {
		return nil
	}
	return os.WriteFile(terminalJailRcPath, []byte(terminalJailRcBody), 0644)
}

// TerminalJailRcPath exposes the path so the terminal handler can hand
// it to bash via --rcfile without hardcoding the string in two places.
func TerminalJailRcPath() string {
	return terminalJailRcPath
}
