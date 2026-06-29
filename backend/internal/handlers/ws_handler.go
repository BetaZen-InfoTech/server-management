package handlers

import (
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/jwt"
	"github.com/gofiber/websocket/v2"
	"github.com/rs/zerolog/log"
)

// NewInstallTerminalWSHandler returns a WebSocket handler for real-time install
// terminal output. The install hub is process-global and broadcasts raw install
// step output (command stdout, hostnames, domains, file paths) to every
// registered client, so the connection MUST be authenticated before Register —
// otherwise any unauthenticated client could subscribe and read install output.
// Installs are owner/admin operations, so only vendor_owner / vendor_admin may
// connect.
func NewInstallTerminalWSHandler(jwtSecret string) func(*websocket.Conn) {
	return func(c *websocket.Conn) {
		// Authenticate via token query parameter before registering on the hub.
		token := c.Query("token")
		if token == "" {
			c.WriteMessage(websocket.TextMessage, []byte("Authentication required"))
			c.Close()
			return
		}

		claims, err := jwt.ValidateToken(jwtSecret, token)
		if err != nil {
			c.WriteMessage(websocket.TextMessage, []byte("Invalid or expired token"))
			c.Close()
			return
		}

		// Install/software ops are owner/admin only.
		if claims.Role != "vendor_owner" && claims.Role != "vendor_admin" {
			c.WriteMessage(websocket.TextMessage, []byte("forbidden"))
			c.Close()
			return
		}

		hub := services.GetInstallHub()
		client := &services.TerminalClient{
			Send: make(chan []byte, 256),
		}
		hub.Register(client)
		defer hub.Unregister(client)

		log.Info().Str("remote", c.RemoteAddr().String()).Str("role", claims.Role).Msg("WebSocket client connected to install terminal")

		// Writer goroutine: send messages from hub to WebSocket
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case msg, ok := <-client.Send:
					if !ok {
						c.WriteMessage(websocket.CloseMessage, []byte{})
						return
					}
					if err := c.WriteMessage(websocket.TextMessage, msg); err != nil {
						return
					}
				case <-ticker.C:
					// Send ping to keep connection alive
					if err := c.WriteMessage(websocket.PingMessage, nil); err != nil {
						return
					}
				}
			}
		}()

		// Reader loop: keep connection alive, read pongs/close
		for {
			_, _, err := c.ReadMessage()
			if err != nil {
				log.Debug().Err(err).Msg("WebSocket client disconnected")
				break
			}
		}
	}
}
