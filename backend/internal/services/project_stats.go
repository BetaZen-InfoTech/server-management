package services

import (
	"context"
	"math"
	"strings"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/bson"
)

// ServiceStat is one deployed service's live resource usage.
type ServiceStat struct {
	MemBytes int64   `json:"mem_bytes"`
	CPUPct   float64 `json:"cpu_pct"`
	State    string  `json:"state"` // active | inactive | failed | activating | static | ""
}

// ProjectStat aggregates every service in a project.
type ProjectStat struct {
	MemBytes int64   `json:"mem_bytes"`
	CPUPct   float64 `json:"cpu_pct"`
	Running  int     `json:"running"`
	Total    int     `json:"total"`
}

// DeployResourceStats is the live resource snapshot for the Deploy Software
// page: per-service (by service id), per-project aggregate (by project id), and
// a fleet total. Static services (served by nginx, no systemd unit) contribute
// state only — they have no dedicated process to measure.
type DeployResourceStats struct {
	Services map[string]ServiceStat `json:"services"`
	Projects map[string]ProjectStat `json:"projects"`
	Totals   ProjectStat            `json:"totals"`
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }

// ServiceStats reads live RAM + CPU for every Deploy Software service from
// systemd's cgroup accounting (one batched, ~1s sample) and rolls it up per
// project. Drives the per-app usage shown on the Deploy Software page.
func (s *ProjectService) ServiceStats(ctx context.Context) (*DeployResourceStats, error) {
	cur, err := s.db.Collection(database.ColProjectServices).Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	var svcs []models.ProjectService
	if err := cur.All(ctx, &svcs); err != nil {
		return nil, err
	}

	units := make([]string, 0, len(svcs))
	for _, sv := range svcs {
		if u := strings.TrimSpace(sv.SystemdUnit); u != "" {
			units = append(units, u)
		}
	}
	unitStats := agent.SystemdUnitStats(ctx, units)

	res := &DeployResourceStats{Services: map[string]ServiceStat{}, Projects: map[string]ProjectStat{}}
	for _, sv := range svcs {
		pid := sv.ProjectID.Hex()
		p := res.Projects[pid]
		p.Total++
		res.Totals.Total++

		var ss ServiceStat
		if us, ok := unitStats[strings.TrimSpace(sv.SystemdUnit)]; ok {
			ss = ServiceStat{MemBytes: us.MemBytes, CPUPct: round1(us.CPUPct), State: us.State}
			if us.State == "active" {
				p.Running++
				res.Totals.Running++
			}
			p.MemBytes += us.MemBytes
			p.CPUPct += us.CPUPct
			res.Totals.MemBytes += us.MemBytes
			res.Totals.CPUPct += us.CPUPct
		} else {
			// Static / no systemd unit — served by nginx, nothing to measure.
			ss.State = "static"
		}
		res.Services[sv.ID.Hex()] = ss
		res.Projects[pid] = p
	}

	for k, v := range res.Projects {
		v.CPUPct = round1(v.CPUPct)
		res.Projects[k] = v
	}
	res.Totals.CPUPct = round1(res.Totals.CPUPct)
	return res, nil
}
