package handlers

import (
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/gofiber/websocket/v2"
	"github.com/rs/zerolog/log"
)

// HandleInstallTerminalWS handles WebSocket connections for real-time install terminal output.
func HandleInstallTerminalWS(c *websocket.Conn) {
	hub := services.GetInstallHub()
	client := &services.TerminalClient{
		Send: make(chan []byte, 256),
	}
	hub.Register(client)
	defer hub.Unregister(client)

	log.Info().Str("remote", c.RemoteAddr().String()).Msg("WebSocket client connected to install terminal")

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
