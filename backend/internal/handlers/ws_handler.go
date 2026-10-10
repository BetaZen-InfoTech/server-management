package handlers

import (
	"context"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/constants"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/jwt"
	"github.com/gofiber/websocket/v2"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
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

// NewImportProgressWSHandler streams live "Import project from JSON" progress
// for ONE job over a websocket (/ws/import-progress/:id). Imports are owner
// operations, so the connection must be authenticated (token query param) and
// only vendor_owner / vendor_admin may connect. The modal fetches the job's
// current services_progress once on mount (the ordinary job poll) for initial
// state, then this stream carries live per-service deltas.
func NewImportProgressWSHandler(jwtSecret string, db *mongo.Database) func(*websocket.Conn) {
	return func(c *websocket.Conn) {
		token := c.Query("token")
		if token == "" {
			c.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","error":"authentication required"}`))
			c.Close()
			return
		}
		claims, err := jwt.ValidateToken(jwtSecret, token)
		if err != nil {
			c.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","error":"invalid or expired token"}`))
			c.Close()
			return
		}
		if claims.Role != "vendor_owner" && claims.Role != "vendor_admin" {
			c.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","error":"forbidden"}`))
			c.Close()
			return
		}
		jobID := c.Params("id")
		if jobID == "" {
			c.Close()
			return
		}

		// Tenant gate — parity with the HTTP GetImportJob: a tenant-scoped
		// caller may only watch an import job belonging to their OWN tenant;
		// vendor_owner sees all. Without this a vendor_admin could stream
		// another tenant's import progress (service names / env-var counts /
		// error summaries) by obtaining a job id. Owner-only scope skips the
		// lookup entirely.
		if constants.IsTenantScoped(claims.Role) {
			oid, perr := primitive.ObjectIDFromHex(jobID)
			if perr != nil {
				c.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","error":"invalid job id"}`))
				c.Close()
				return
			}
			var job models.ProjectImportJob
			dctx, dcancel := context.WithTimeout(context.Background(), 5*time.Second)
			derr := db.Collection(database.ColProjectImportJobs).FindOne(dctx, bson.M{"_id": oid}).Decode(&job)
			dcancel()
			if derr != nil || (job.TenantID != primitive.NilObjectID && (claims.TenantID == "" || job.TenantID.Hex() != claims.TenantID)) {
				c.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","error":"forbidden"}`))
				c.Close()
				return
			}
		}

		hub := services.GetImportProgressHub()
		client := &services.ImportProgressClient{Send: make(chan []byte, 256)}
		hub.Register(jobID, client)
		defer hub.Unregister(jobID, client)

		log.Info().Str("job_id", jobID).Str("role", claims.Role).Msg("WebSocket client watching import progress")

		// Writer goroutine: hub → websocket. (Only this goroutine writes, so
		// there are no concurrent writes to the conn.)
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
					if err := c.WriteMessage(websocket.PingMessage, nil); err != nil {
						return
					}
				}
			}
		}()

		// Reader loop keeps the connection alive until the client disconnects.
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				break
			}
		}
	}
}
