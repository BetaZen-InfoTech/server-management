package handlers

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/pkg/jwt"
	"github.com/creack/pty"
	"github.com/gofiber/websocket/v2"
	"github.com/rs/zerolog/log"
)

type resizeMsg struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// NewTerminalWSHandler returns a WebSocket handler for interactive terminal sessions.
// WHM vendors get root shell, cPanel customers get their own Linux user shell.
func NewTerminalWSHandler(jwtSecret string) func(*websocket.Conn) {
	return func(c *websocket.Conn) {
		// Authenticate via token query parameter
		token := c.Query("token")
		if token == "" {
			c.WriteMessage(websocket.TextMessage, []byte("\r\n\x1b[31mError: Authentication required\x1b[0m\r\n"))
			c.Close()
			return
		}

		claims, err := jwt.ValidateToken(jwtSecret, token)
		if err != nil {
			c.WriteMessage(websocket.TextMessage, []byte("\r\n\x1b[31mError: Invalid or expired token\x1b[0m\r\n"))
			c.Close()
			return
		}

		// Determine shell command based on role
		var cmd *exec.Cmd
		switch claims.Role {
		case "vendor_owner", "vendor_admin", "developer", "support":
			// WHM vendor users get root shell
			cmd = exec.Command("/bin/bash", "--login")
			cmd.Env = append(os.Environ(),
				"TERM=xterm-256color",
				"HOME=/root",
				"USER=root",
			)
			cmd.Dir = "/root"
		case "customer":
			// cPanel customers get their own Linux user shell
			username := claims.Email
			// Use the part before @ as the Linux username
			for i, ch := range username {
				if ch == '@' {
					username = username[:i]
					break
				}
			}
			cmd = exec.Command("/bin/su", "-", username)
			cmd.Env = append(os.Environ(), "TERM=xterm-256color")
		default:
			c.WriteMessage(websocket.TextMessage, []byte("\r\n\x1b[31mError: Unauthorized role\x1b[0m\r\n"))
			c.Close()
			return
		}

		// Start command with PTY
		ptmx, err := pty.Start(cmd)
		if err != nil {
			log.Error().Err(err).Str("role", claims.Role).Msg("Failed to start PTY")
			c.WriteMessage(websocket.TextMessage, []byte("\r\n\x1b[31mError: Failed to start terminal session\x1b[0m\r\n"))
			c.Close()
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
