package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/jwt"
	"github.com/creack/pty"
	"github.com/gofiber/websocket/v2"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

// buildJailedShellCmd returns an exec.Cmd that launches an interactive
// bash for the given linux user, but with the in-shell sandbox rcfile
// sourced so `cd` refuses paths outside $HOME. Wrapped via `su - user
// -c "exec bash --rcfile ... -i"` so su handles the uid/gid switch and
// login env, and exec then replaces that login shell with the sandboxed
// bash (the PTY stays attached).
//
// If the rcfile is missing (install still running, or EnsureTerminalJailRcfile
// failed on a read-only /etc), the handler should fall back to a plain
// `su - user` — cheaper than failing the session entirely.
func buildJailedShellCmd(targetUser string) *exec.Cmd {
	rcfile := agent.TerminalJailRcPath()
	// Quote nothing fancy — the rcfile path is a Go constant and can't
	// contain shell metacharacters. targetUser has already been
	// validated upstream (id lookup or tenant membership check).
	script := fmt.Sprintf("exec bash --rcfile %s -i", rcfile)
	cmd := exec.Command("/bin/su", "-", targetUser, "-c", script)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	return cmd
}

// jailRcfileExists reports whether the sandbox rcfile is on disk. The
// terminal handler checks this before calling buildJailedShellCmd;
// false = fall back to the old su-only path.
func jailRcfileExists() bool {
	_, err := os.Stat(agent.TerminalJailRcPath())
	return err == nil
}

type resizeMsg struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// WebSocket close codes in the private-use range (4000-4999) that the
// terminal uses to tell the browser WHY a session was refused. The
// browser reads these off CloseEvent.code and renders a specific,
// actionable message.
const (
	wsCloseAuthRequired = 4401 // no token supplied
	wsCloseAuthExpired  = 4403 // token invalid or past its exp
	wsCloseAccountGone  = 4404 // suspended / soft-deleted account
	wsCloseForbidden    = 4405 // authenticated but not allowed this shell
	wsCloseInternal     = 4500 // pty / shell spawn failure
)

// wsReject reports a fatal session error to the browser and closes the
// socket cleanly.
//
// The previous shape of these call sites was:
//
//	c.WriteMessage(websocket.TextMessage, []byte("...error..."))
//	c.Close()
//
// which closes the TCP connection WITHOUT a WebSocket close handshake.
// The browser sees an abnormal closure (code 1006), fires onerror before
// onclose, and routinely discards the just-written text frame that was
// still in flight — so the operator got a bare "Connection error." with
// no hint that the real problem was an expired token. Verified against
// the running panel: the text frame arrives reliably for a client that
// reads eagerly (curl) and is lost for the browser often enough that the
// terminal looked simply broken.
//
// Sending a real close frame carrying the code and reason makes the
// failure survive the teardown, and gives the frontend something
// structured to branch on rather than a string it has to guess at.
func wsReject(c *websocket.Conn, code int, reason string) {
	// Still write the human-readable line: it renders inline in xterm for
	// anyone watching the terminal itself.
	_ = c.WriteMessage(websocket.TextMessage, []byte("\r\n\x1b[31m"+reason+"\x1b[0m\r\n"))
	_ = c.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(code, reason),
		time.Now().Add(2*time.Second),
	)
	_ = c.Close()
}

// NewTerminalWSHandler returns a WebSocket handler for interactive terminal sessions.
// vendor_owner gets a root shell, all other vendor roles get a shell as their
// own tenant-scoped linux user (validated against their tenant). cPanel
// customers get their own Linux user shell.
func NewTerminalWSHandler(jwtSecret string, db *mongo.Database) func(*websocket.Conn) {
	return func(c *websocket.Conn) {
		// Authenticate via token query parameter
		token := c.Query("token")
		if token == "" {
			wsReject(c, wsCloseAuthRequired, "Error: Authentication required")
			return
		}

		claims, err := jwt.ValidateToken(jwtSecret, token)
		if err != nil {
			// Overwhelmingly this is a merely EXPIRED access token rather
			// than a forged one: access tokens live 15 minutes, the API
			// client only refreshes reactively on a 401, and the Terminal
			// page issues no API call of its own — so opening it on a
			// stale tab hands us a token nothing has had cause to renew.
			// The dedicated close code lets the browser refresh and retry
			// once instead of showing the operator a dead terminal.
			wsReject(c, wsCloseAuthExpired, "Error: Invalid or expired token")
			return
		}

		// Account status check: a suspended (is_active=false) or soft-deleted
		// (deleted_at set) user must NOT keep a working shell via an existing
		// valid JWT. Mirrors middleware.Auth's isUserAllowed gate, which this
		// WebSocket route otherwise bypasses entirely.
		{
			statusCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			oid, oidErr := primitive.ObjectIDFromHex(claims.UserID)
			var u struct {
				IsActive  bool        `bson:"is_active"`
				DeletedAt interface{} `bson:"deleted_at"`
			}
			err := mongo.ErrNoDocuments
			if oidErr == nil {
				err = db.Collection(database.ColUsers).FindOne(statusCtx, bson.M{"_id": oid}).Decode(&u)
			}
			cancel()
			if oidErr != nil || err != nil || !u.IsActive || u.DeletedAt != nil {
				wsReject(c, wsCloseAccountGone, "Error: account suspended or deleted")
				return
			}
		}

		// Determine shell command based on role
		var cmd *exec.Cmd
		switch claims.Role {
		case "vendor_owner":
			// Platform owner: full root shell, optionally su into a named user.
			targetUser := c.Query("user")
			if targetUser == "" || targetUser == "root" {
				cmd = exec.Command("/bin/bash", "--login")
				cmd.Env = append(os.Environ(),
					"TERM=xterm-256color",
					"HOME=/root",
					"USER=root",
				)
				cmd.Dir = "/root"
			} else {
				// Validate the target user actually exists on the system before
				// spawning su — otherwise the browser sees an opaque
				// `su: user X does not exist or the user entry does not contain
				// all the required fields` and disconnects, which is exactly the
				// failure mode that bit operators when the frontend defaulted
				// to email-prefix usernames like "admin" that aren't Linux
				// accounts. Falling back to root lets the panel owner still
				// get a usable shell instead of a hard close.
				if _, lookupErr := exec.Command("id", targetUser).CombinedOutput(); lookupErr != nil {
					c.WriteMessage(websocket.TextMessage, []byte(
						fmt.Sprintf("\r\n\x1b[33mNote: Linux user %q does not exist. Opening root shell instead.\x1b[0m\r\n", targetUser),
					))
					cmd = exec.Command("/bin/bash", "--login")
					cmd.Env = append(os.Environ(),
						"TERM=xterm-256color",
						"HOME=/root",
						"USER=root",
					)
					cmd.Dir = "/root"
				} else {
					cmd = exec.Command("/bin/su", "-", targetUser)
					cmd.Env = append(os.Environ(), "TERM=xterm-256color")
				}
			}
		case "vendor_admin":
			// vendor_admin is a reseller / tenant owner — they manage
			// customers under their tenant and legitimately need to see
			// /home/<customer>/ paths of the accounts they own. Not
			// sandboxed; gets a plain login shell as their own user.
			bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			target := c.Query("user")
			if target == "" || target == "root" {
				own, lerr := services.LookupOwnUsername(bgCtx, db, claims.UserID)
				if lerr != nil || own == "" {
					wsReject(c, wsCloseForbidden, "Error: no linux user is provisioned for your account")
					return
				}
				target = own
			} else {
				if aerr := services.AssertUsernameInTenant(bgCtx, db, claims.Role, claims.TenantID, target); aerr != nil {
					wsReject(c, wsCloseForbidden,
						fmt.Sprintf("Error: forbidden: user %q is not in your tenant", target))
					return
				}
			}
			cmd = exec.Command("/bin/su", "-", target)
			cmd.Env = append(os.Environ(), "TERM=xterm-256color")

		case "vendor_staff", "developer", "support":
			// Tenant-scoped non-owner vendor roles: NEVER root, and
			// sandbox-shelled. Default to the caller's own linux user;
			// explicit ?user= must belong to the same tenant. The
			// sandbox rcfile refuses `cd /etc` and friends so an
			// accidental click can't expose other tenants' data.
			bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			target := c.Query("user")
			if target == "" || target == "root" {
				own, lerr := services.LookupOwnUsername(bgCtx, db, claims.UserID)
				if lerr != nil || own == "" {
					wsReject(c, wsCloseForbidden, "Error: no linux user is provisioned for your account")
					return
				}
				target = own
			} else {
				if aerr := services.AssertUsernameInTenant(bgCtx, db, claims.Role, claims.TenantID, target); aerr != nil {
					wsReject(c, wsCloseForbidden,
						fmt.Sprintf("Error: forbidden: user %q is not in your tenant", target))
					return
				}
			}
			if jailRcfileExists() {
				cmd = buildJailedShellCmd(target)
			} else {
				cmd = exec.Command("/bin/su", "-", target)
				cmd.Env = append(os.Environ(), "TERM=xterm-256color")
			}

		case "customer":
			// cPanel customers: sandboxed shell that lands in $HOME and
			// refuses cd outside it. The user's login shell is usually
			// jailkit's jk_chrootsh (set by agent.JailUser at user
			// creation), so they're ALSO chroot-jailed; the bash rcfile
			// is a defense-in-depth layer for accounts where jailkit
			// couldn't be installed.
			username := claims.Email
			for i, ch := range username {
				if ch == '@' {
					username = username[:i]
					break
				}
			}
			// Verify the linux user exists before spawning — su fails
			// with an opaque "Unknown id" otherwise that the browser
			// shows as an abrupt disconnect.
			if _, lookupErr := exec.Command("id", username).CombinedOutput(); lookupErr != nil {
				wsReject(c, wsCloseForbidden,
					fmt.Sprintf("Error: Linux user %q not provisioned", username))
				return
			}
			if jailRcfileExists() {
				cmd = buildJailedShellCmd(username)
			} else {
				cmd = exec.Command("/bin/su", "-", username)
				cmd.Env = append(os.Environ(), "TERM=xterm-256color")
			}
		default:
			wsReject(c, wsCloseForbidden, "Error: Unauthorized role")
			return
		}

		// Start command with PTY
		ptmx, err := pty.Start(cmd)
		if err != nil {
			log.Error().Err(err).Str("role", claims.Role).Msg("Failed to start PTY")
			wsReject(c, wsCloseInternal, "Error: Failed to start terminal session")
			return
		}

		log.Info().
			Str("user", claims.Email).
			Str("role", claims.Role).
			Str("remote", c.RemoteAddr().String()).
			Msg("Terminal session started")

		// Set initial terminal size
		pty.Setsize(ptmx, &pty.Winsize{Cols: 120, Rows: 40})

		var once sync.Once
		cleanup := func() {
			once.Do(func() {
				ptmx.Close()
				if cmd.Process != nil {
					cmd.Process.Kill()
				}
				cmd.Wait()
				log.Info().Str("user", claims.Email).Msg("Terminal session ended")
			})
		}
		defer cleanup()

		// PTY → WebSocket (send terminal output to browser)
		go func() {
			buf := make([]byte, 4096)
			for {
				n, err := ptmx.Read(buf)
				if err != nil {
					if err != io.EOF {
						log.Debug().Err(err).Msg("PTY read error")
					}
					c.WriteMessage(websocket.CloseMessage, []byte{})
					return
				}
				if err := c.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
					log.Debug().Err(err).Msg("WebSocket write error")
					return
				}
			}
		}()

		// Keep-alive pings
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				if err := c.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			}
		}()

		// WebSocket → PTY (send user input to shell)
		// Protocol: first byte determines message type
		//   0x00 = terminal input (rest is stdin data)
		//   0x01 = resize (rest is JSON {"cols":80,"rows":24})
		for {
			msgType, msg, err := c.ReadMessage()
			if err != nil {
				log.Debug().Err(err).Msg("WebSocket read error")
				break
			}

			if msgType == websocket.CloseMessage {
				break
			}

			if len(msg) == 0 {
				continue
			}

			switch msg[0] {
			case 0: // Terminal input
				if len(msg) > 1 {
					if _, err := ptmx.Write(msg[1:]); err != nil {
						log.Debug().Err(err).Msg("PTY write error")
						return
					}
				}
			case 1: // Resize
				if len(msg) > 1 {
					var rs resizeMsg
					if err := json.Unmarshal(msg[1:], &rs); err == nil && rs.Cols > 0 && rs.Rows > 0 {
						pty.Setsize(ptmx, &pty.Winsize{Cols: rs.Cols, Rows: rs.Rows})
					}
				}
			default:
				// Legacy: treat as raw input for compatibility
				if _, err := ptmx.Write(msg); err != nil {
					log.Debug().Err(err).Msg("PTY write error")
					return
				}
			}
		}
	}
}
