package services

import (
	"context"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// RunBootReconcile sweeps every panel-managed systemd unit on startup
// and brings it back to its expected state. Two failure modes this
// covers, both of which leave the operator staring at a "stopped" page
// after every VPS reboot:
//
//  1. The unit was never `systemctl enable`d (older deploys created
//     before we added `enable` to the create path) — `systemctl start`
//     during the boot sequence skipped it. We re-enable + start.
//  2. The unit IS enabled but was racing the network / mongo and
//     systemd hit the start-limit ceiling, leaving it `failed`. We
//     `reset-failed` then start.
//
// Idempotent and best-effort: a unit that's already running stays
// running; a unit whose .service file has been hand-removed gets a
// quiet warning and is otherwise skipped.
//
// Run as a goroutine from main.go after all services are wired but
// before the HTTP listener accepts traffic — these are localhost
// systemctl calls and finish in well under a second per unit, so even
// 200 apps complete in a couple of seconds.
func RunBootReconcile(ctx context.Context, db *mongo.Database) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	reconciled, failed := 0, 0

	// --- Apps (sp-app-<name>.service) ---
	if cur, err := db.Collection(database.ColApps).Find(ctx, bson.M{}); err == nil {
		var rows []struct {
			Name   string `bson:"name"`
			Status string `bson:"status"`
		}
		if cerr := cur.All(ctx, &rows); cerr == nil {
			for _, r := range rows {
				if r.Name == "" {
					continue
				}
				// Skip apps that the operator deliberately stopped — only
				// resurrect things that were running (or expected to be).
				// "stopped" here is a deliberate operator action recorded
				// after a Stop click; "failed" / "running" / "" all mean
				// "should be up".
				if r.Status == "stopped" {
					continue
				}
				if reconcileUnit(ctx, "sp-app-"+r.Name) {
					reconciled++
				} else {
					failed++
				}
			}
		}
		cur.Close(ctx)
	}

	// --- Project services (unit name stored on the row) ---
	if cur, err := db.Collection(database.ColProjectServices).Find(ctx, bson.M{}); err == nil {
		var rows []struct {
			SystemdUnit string `bson:"systemd_unit"`
			Status      string `bson:"status"`
		}
		if cerr := cur.All(ctx, &rows); cerr == nil {
			for _, r := range rows {
				if r.SystemdUnit == "" || r.Status == "stopped" {
					continue
				}
				if reconcileUnit(ctx, r.SystemdUnit) {
					reconciled++
				} else {
					failed++
				}
			}
		}
		cur.Close(ctx)
	}

	if reconciled+failed > 0 {
		// stdout — picked up by the panel's own systemd journal.
		println("[boot-reconcile] swept panel-managed units:",
			"reconciled=", reconciled, "failed=", failed)
	}
}

// reconcileUnit performs the per-unit recovery: enable if not enabled,
// reset-failed if currently failed, then start if not active. Returns
// true if the unit ends in an `active` state; false otherwise.
func reconcileUnit(ctx context.Context, unit string) bool {
	// Probe presence first — `systemctl list-unit-files` returns the
	// file name when it exists. Avoids a noisy warning for apps whose
	// .service file was hand-removed but the mongo row lingers.
	probe, _ := agent.RunCommand(ctx, "systemctl", "list-unit-files", unit+".service")
	if probe == nil || !strings.Contains(probe.Output, unit+".service") {
		return false
	}

	// Idempotent enable — no-op if already enabled.
	agent.RunCommand(ctx, "systemctl", "enable", unit)

	// Clear any lingering "failed" so a previous start-limit hit doesn't
	// block this attempt.
	agent.RunCommand(ctx, "systemctl", "reset-failed", unit)

	// Start (no-op if already active). Use start, not restart, so we
	// don't bounce a process that's already healthy.
	agent.RunCommand(ctx, "systemctl", "start", unit)

	// Verify.
	chk, _ := agent.RunCommand(ctx, "systemctl", "is-active", unit)
	if chk != nil && strings.TrimSpace(chk.Output) == "active" {
		return true
	}
	return false
}
