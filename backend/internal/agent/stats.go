package agent

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// UnitStat is one systemd unit's live resource usage, read from systemd's own
// cgroup accounting (so it covers the unit's whole process tree — e.g. the
// npm + sh + next-server chain — not just MainPID).
type UnitStat struct {
	Unit     string  `json:"unit"`
	MemBytes int64   `json:"mem_bytes"`
	CPUPct   float64 `json:"cpu_pct"`
	State    string  `json:"state"`
	PID      int     `json:"pid"`
}

// parseShowBlocks splits `systemctl show unitA unitB …` output (one Key=Value
// per line, units separated by a blank line) into a map keyed by each block's
// Id= value.
func parseShowBlocks(text string) map[string]map[string]string {
	res := map[string]map[string]string{}
	for _, block := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n") {
		cur := map[string]string{}
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if k, v, ok := strings.Cut(line, "="); ok {
				cur[strings.TrimSpace(k)] = strings.TrimSpace(v)
			}
		}
		if id := cur["Id"]; id != "" {
			res[id] = cur
		}
	}
	return res
}

func parseInt64(s string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// SystemdUnitStats returns per-unit memory (cgroup MemoryCurrent — the whole
// process tree) and CPU% for the given units. CPU% is derived from the delta of
// CPUUsageNSec over a ~700ms sample window, so it reflects recent activity and
// can exceed 100% on a multi-core box. One cheap `systemctl show` covers all
// units at once (≈0.2s for ~120 units), so this samples twice and is still ~1s.
// Units that aren't running report their state with zero usage.
func SystemdUnitStats(ctx context.Context, units []string) map[string]UnitStat {
	out := map[string]UnitStat{}
	clean := make([]string, 0, len(units))
	seen := map[string]bool{}
	for _, u := range units {
		u = strings.TrimSpace(u)
		if u != "" && !seen[u] {
			seen[u] = true
			clean = append(clean, u)
		}
	}
	if len(clean) == 0 {
		return out
	}

	args := append([]string{"show", "-p", "Id", "-p", "MemoryCurrent", "-p", "CPUUsageNSec", "-p", "ActiveState", "-p", "MainPID"}, clean...)
	r0, err := RunCommand(ctx, "systemctl", args...)
	if err != nil || r0 == nil {
		return out
	}
	t0 := time.Now()
	b0 := parseShowBlocks(r0.Output)

	time.Sleep(700 * time.Millisecond)

	r1, _ := RunCommand(ctx, "systemctl", append([]string{"show", "-p", "Id", "-p", "CPUUsageNSec"}, clean...)...)
	var b1 map[string]map[string]string
	if r1 != nil {
		b1 = parseShowBlocks(r1.Output)
	}
	interval := time.Since(t0).Nanoseconds()
	if interval <= 0 {
		interval = int64(700 * time.Millisecond)
	}

	for unit, props := range b0 {
		st := UnitStat{
			Unit:     unit,
			State:    props["ActiveState"],
			MemBytes: parseInt64(props["MemoryCurrent"]),
			PID:      int(parseInt64(props["MainPID"])),
		}
		// MemoryCurrent is a near-uint64-max sentinel when unset (stopped unit);
		// a parse of that overflows to 0 above, but clamp any absurd value too.
		if st.MemBytes < 0 || st.MemBytes > (int64(1)<<62) {
			st.MemBytes = 0
		}
		if b1 != nil {
			if p1, ok := b1[unit]; ok {
				cpu0 := parseInt64(props["CPUUsageNSec"])
				cpu1 := parseInt64(p1["CPUUsageNSec"])
				if cpu1 >= cpu0 && cpu0 > 0 {
					st.CPUPct = float64(cpu1-cpu0) / float64(interval) * 100.0
				}
			}
		}
		out[unit] = st
	}
	return out
}
