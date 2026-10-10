package services

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

// ImportProgressMsg is one live event streamed to websocket subscribers of an
// import job.
type ImportProgressMsg struct {
	Type       string `json:"type"` // "stage" | "service" | "done"
	Stage      string `json:"stage,omitempty"`
	Name       string `json:"name,omitempty"`
	Index      int    `json:"index,omitempty"`
	Total      int    `json:"total,omitempty"`
	Phase      string `json:"phase,omitempty"`  // building | done | failed
	Status     string `json:"status,omitempty"` // running | needs_env_vars | error
	MissingEnv int    `json:"missing_env,omitempty"`
	Error      string `json:"error,omitempty"`
	JobStatus  string `json:"job_status,omitempty"` // on "done": completed | failed
	Timestamp  string `json:"timestamp"`
}

// ImportProgressClient is one websocket subscriber for a specific import job.
type ImportProgressClient struct {
	Send chan []byte
}

// ImportProgressHub fans out import progress events keyed by job id, so a
// connection only receives events for the job it is watching. Mirrors
// TerminalHub but topic-scoped.
type ImportProgressHub struct {
	mu     sync.RWMutex
	topics map[string]map[*ImportProgressClient]bool
}

var importProgressHub = &ImportProgressHub{topics: make(map[string]map[*ImportProgressClient]bool)}

// GetImportProgressHub returns the process-global import-progress hub.
func GetImportProgressHub() *ImportProgressHub { return importProgressHub }

// Register subscribes a client to one job's progress stream.
func (h *ImportProgressHub) Register(jobID string, c *ImportProgressClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.topics[jobID] == nil {
		h.topics[jobID] = make(map[*ImportProgressClient]bool)
	}
	h.topics[jobID][c] = true
}

// Unregister removes a client and closes its channel.
func (h *ImportProgressHub) Unregister(jobID string, c *ImportProgressClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if m := h.topics[jobID]; m != nil {
		if _, ok := m[c]; ok {
			delete(m, c)
			close(c.Send)
		}
		if len(m) == 0 {
			delete(h.topics, jobID)
		}
	}
}

// Publish broadcasts one event to every client watching jobID. Slow clients
// (full buffer) are skipped, never blocked.
func (h *ImportProgressHub) Publish(jobID string, msg ImportProgressMsg) {
	msg.Timestamp = time.Now().Format(time.RFC3339)
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.topics[jobID] {
		select {
		case c.Send <- data:
		default: // slow client — drop this frame rather than block the import
		}
	}
}

// importProgressCtxKey carries the per-import reporter through Provision.
type importProgressCtxKey struct{}

// ImportProgressReporter receives per-stage + per-service progress during an
// import. nil when an import runs outside the async job path (a normal
// New-Project Provision), in which case Provision skips reporting entirely.
type ImportProgressReporter interface {
	Stage(stage string)
	Service(index, total int, name, phase, status string, missingEnv int, errMsg string)
}

// WithImportProgress attaches a reporter so Provision can emit live progress.
func WithImportProgress(ctx context.Context, r ImportProgressReporter) context.Context {
	return context.WithValue(ctx, importProgressCtxKey{}, r)
}

// importProgressFrom returns the attached reporter, or nil. Every caller in
// Provision MUST nil-check — the reporter is a pure side-effect sink and must
// never alter provisioning control flow.
func importProgressFrom(ctx context.Context) ImportProgressReporter {
	if r, ok := ctx.Value(importProgressCtxKey{}).(ImportProgressReporter); ok {
		return r
	}
	return nil
}

// reportImportStage / reportImportService are nil-safe helpers Provision uses
// so the hook sites stay one line and can never panic when no reporter is set.
func reportImportStage(ctx context.Context, stage string) {
	if r := importProgressFrom(ctx); r != nil {
		r.Stage(stage)
	}
}

func reportImportService(ctx context.Context, index, total int, name, phase, status string, missingEnv int, errMsg string) {
	if r := importProgressFrom(ctx); r != nil {
		r.Service(index, total, name, phase, status, missingEnv, errMsg)
	}
}

// jobProgressReporter persists progress onto the import-job row AND streams it
// over the hub. It owns a local snapshot so each write carries the full current
// list (simple + consistent for a small service count).
type jobProgressReporter struct {
	db    *mongo.Database
	hub   *ImportProgressHub
	jobID primitive.ObjectID
	mu    sync.Mutex
	svcs  []models.ImportServiceProgress
}

func (r *jobProgressReporter) persist(set bson.M) {
	set["updated_at"] = time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r.db.Collection(database.ColProjectImportJobs).UpdateOne(ctx, bson.M{"_id": r.jobID}, bson.M{"$set": set})
}

func (r *jobProgressReporter) Stage(stage string) {
	r.persist(bson.M{"stage": stage})
	r.hub.Publish(r.jobID.Hex(), ImportProgressMsg{Type: "stage", Stage: stage})
}

func (r *jobProgressReporter) Service(index, total int, name, phase, status string, missingEnv int, errMsg string) {
	r.mu.Lock()
	entry := models.ImportServiceProgress{Name: name, Index: index, Total: total, Phase: phase, Status: status, MissingEnv: missingEnv, Error: errMsg}
	found := false
	for i := range r.svcs {
		if r.svcs[i].Name == name {
			r.svcs[i] = entry
			found = true
			break
		}
	}
	if !found {
		r.svcs = append(r.svcs, entry)
	}
	snapshot := append([]models.ImportServiceProgress(nil), r.svcs...)
	r.mu.Unlock()

	r.persist(bson.M{"services_progress": snapshot})
	r.hub.Publish(r.jobID.Hex(), ImportProgressMsg{Type: "service", Name: name, Index: index, Total: total, Phase: phase, Status: status, MissingEnv: missingEnv, Error: errMsg})
}
