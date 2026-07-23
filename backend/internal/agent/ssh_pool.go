package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// ============================================================================
// Connection reuse
// ============================================================================
//
// Every agent.Discover* / SSHCommand call used to open its OWN TCP
// connection, run one command, and tear it down. Against a remote box a
// single dial (TCP + SSH handshake + auth) measured ~1.8-2.5s on a real
// customer server — so the transfer wizard's 15-probe discovery pass
// burned ~28s in handshakes alone before running a single useful byte of
// shell. That is most of a reverse proxy's 60s budget, which is how
// POST /transfers/discover came to return 504.
//
// WithSSHConnection dials ONCE and stashes the client on the context.
// SSHCommand (and everything layered on it) transparently reuses that
// client, opening a cheap multiplexed channel per command instead of a
// new connection. Call sites keep their existing signatures — they just
// need to run under the derived context.
//
// Concurrency is capped because sshd's default MaxSessions is 10; opening
// more channels than that on one connection gets them refused. We stay
// well under it and fall back to a fresh dial if the shared connection
// dies mid-pass.

// maxPooledSessions bounds concurrent channels on a single shared
// connection. sshd's default MaxSessions is 10; 6 leaves headroom for
// the operator's own sessions and for sshd configs that lowered it.
const maxPooledSessions = 6

// defaultCommandTimeout is the ceiling for a single remote command when
// the caller's context carries no deadline of its own. Without this a
// hung remote command (a `du` over a 40 GB home, a `mysql` blocked on a
// lock, an NFS stat on a dead mount) pinned the request goroutine
// forever — Fiber had nothing to cancel and the client just waited for
// the proxy to give up.
const defaultCommandTimeout = 90 * time.Second

type sshClientContextKey struct{}

// pooledConn is a shared ssh.Client plus the identity it was dialled
// with. Identity is checked on reuse so a context carrying a connection
// to host A can never silently execute a command aimed at host B.
type pooledConn struct {
	client *ssh.Client
	host   string
	port   int
	user   string

	sem chan struct{}

	mu     sync.Mutex
	broken bool
}

// WithSSHConnection dials host once and returns a context that reuses the
// resulting connection for every subsequent SSHCommand. The returned
// close func must be called by the caller (defer it) to release the
// connection.
//
// On dial failure the ORIGINAL context is returned along with the error;
// callers that prefer best-effort behaviour can ignore the error and
// carry on with per-command dialling.
func WithSSHConnection(ctx context.Context, host string, port int, user, pass string) (context.Context, func(), error) {
	client, err := sshDial(ctx, host, port, user, pass)
	if err != nil {
		return ctx, func() {}, err
	}
	pc := &pooledConn{
		client: client,
		host:   host,
		port:   port,
		user:   user,
		sem:    make(chan struct{}, maxPooledSessions),
	}
	closeFn := func() { _ = client.Close() }
	return context.WithValue(ctx, sshClientContextKey{}, pc), closeFn, nil
}

// pooledFor returns the shared connection on ctx if it matches the target
// identity, else nil.
func pooledFor(ctx context.Context, host string, port int, user string) *pooledConn {
	pc, _ := ctx.Value(sshClientContextKey{}).(*pooledConn)
	if pc == nil {
		return nil
	}
	if pc.host != host || pc.port != port {
		return nil
	}
	// An empty user on the call side means "whatever the connection was
	// opened with" — the Discover* helpers pass the resolved user through,
	// but some callers pass "" when the key/context already pins it.
	if user != "" && pc.user != user {
		return nil
	}
	pc.mu.Lock()
	broken := pc.broken
	pc.mu.Unlock()
	if broken {
		return nil
	}
	return pc
}

func (pc *pooledConn) markBroken() {
	pc.mu.Lock()
	pc.broken = true
	pc.mu.Unlock()
}

// runSession executes command on an already-open connection, respecting
// ctx cancellation. Returns errSessionUnusable if the underlying
// connection has gone away, which tells the caller to fall back to a
// fresh dial.
var errSessionUnusable = errors.New("pooled ssh connection unusable")

func (pc *pooledConn) run(ctx context.Context, command string) (*CommandResult, error) {
	select {
	case pc.sem <- struct{}{}:
		defer func() { <-pc.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	session, err := pc.client.NewSession()
	if err != nil {
		// Don't poison the pool just because ctx expired while we waited
		// for a slot — the connection is almost certainly still fine, and
		// marking it broken would send every remaining probe down the
		// slow per-command dial path for no reason.
		if ctx.Err() == nil {
			pc.markBroken()
		}
		return nil, fmt.Errorf("%w: %v", errSessionUnusable, err)
	}
	return runSessionCtx(ctx, session, command)
}

// runSessionCtx runs command on session and aborts it when ctx is done.
//
// golang.org/x/crypto/ssh has no context-aware Run, so we race the run
// against ctx.Done() and close the session to unblock it. Closing is what
// actually tears down the channel — without it the goroutine leaks and
// the remote command keeps running.
func runSessionCtx(ctx context.Context, session *ssh.Session, command string) (*CommandResult, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultCommandTimeout)
		defer cancel()
	}

	var stdout, stderr syncBuffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	done := make(chan error, 1)
	go func() { done <- session.Run(command) }()

	select {
	case err := <-done:
		_ = session.Close()
		result := &CommandResult{Output: stdout.String(), Error: stderr.String()}
		if err != nil {
			var exitErr *ssh.ExitError
			if errors.As(err, &exitErr) {
				result.ExitCode = exitErr.ExitStatus()
				return result, nil
			}
			return result, fmt.Errorf("ssh exec failed: %w", err)
		}
		return result, nil
	case <-ctx.Done():
		// Close unblocks session.Run in the goroutine above.
		_ = session.Close()
		<-done
		// Hand back whatever the command managed to emit before the
		// deadline — partial output is more useful than none for the
		// discovery probes, and callers that care check the error.
		return &CommandResult{Output: stdout.String(), Error: stderr.String()},
			fmt.Errorf("remote command timed out: %w", ctx.Err())
	}
}

// syncBuffer is a bytes.Buffer guarded by a mutex. The ssh package writes
// to Stdout/Stderr from its own goroutine, so reading them after a
// timeout (while that goroutine may still be writing) is a data race
// without this.
type syncBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
