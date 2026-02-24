package services

import (
	"encoding/json"
	"sync"
	"time"
)

// TerminalMessage represents a single line/event sent to WebSocket clients.
type TerminalMessage struct {
	Type      string `json:"type"`      // step_start, step_output, step_complete, step_error, install_complete, install_failed
	Step      int    `json:"step"`      // step index
	StepName  string `json:"step_name"` // human-readable step name
	Output    string `json:"output"`    // terminal text output
	Error     string `json:"error,omitempty"`
	Total     int    `json:"total"`     // total steps
	Timestamp string `json:"timestamp"`
}

// TerminalClient is a single WebSocket subscriber.
type TerminalClient struct {
	Send chan []byte
}

// TerminalHub manages WebSocket clients subscribed to installation terminal output.
type TerminalHub struct {
	mu      sync.RWMutex
	clients map[*TerminalClient]bool
}

// Global terminal hub singleton
var installHub = &TerminalHub{
	clients: make(map[*TerminalClient]bool),
}

// GetInstallHub returns the global installation terminal hub.
func GetInstallHub() *TerminalHub {
	return installHub
}

// Register adds a new client.
func (h *TerminalHub) Register(c *TerminalClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = true
}

// Unregister removes a client and closes its channel.
func (h *TerminalHub) Unregister(c *TerminalClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		close(c.Send)
	}
}

// Broadcast sends a message to all connected clients.
func (h *TerminalHub) Broadcast(msg TerminalMessage) {
	msg.Timestamp = time.Now().Format(time.RFC3339)
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	for client := range h.clients {
		select {
		case client.Send <- data:
		default:
			// Client buffer full, skip
		}
	}
}
