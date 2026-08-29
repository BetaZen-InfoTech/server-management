package services

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Whole-server disaster-recovery (DR) bundles are produced by the shell
// scripts/bzpanel-backup.sh (NOT the per-domain Go BackupService), which tars
// the panel Mongo brain + /etc + /home + configs into
// bzpanel-dr-<host>-<ts>.tar.gz[.enc] under a local directory, with .manifest.json
// and .sha256 sidecars. These files are NOT tracked in Mongo, so the normal
// Backups list can't see them. This surfaces them to the WHM Backups page so the
// owner can list / download / delete them (they are large — tens of GB each — and
// were silently filling the disk when retention lagged).
const (
	drBackupDirDefault = "/var/backups/bzpanel"
	drBackupConfFile   = "/etc/bzpanel/backup.conf"
	drBackupPrefix     = "bzpanel-dr-"
)

// DRBackup is one whole-server bundle on local disk.
type DRBackup struct {
	Name       string    `json:"name"`
	Size       int64     `json:"size"`
	SizeHuman  string    `json:"size_human"`
	ModifiedAt time.Time `json:"modified_at"`
	Encrypted  bool      `json:"encrypted"`
}

// drBackupDir resolves where DR bundles live: BZ_BACKUP_LOCAL_DIR from
// /etc/bzpanel/backup.conf when the operator set it, else the default. Mirrors
// the LOCAL_DIR resolution in scripts/bzpanel-backup.sh so the panel reads the
// exact directory the cron writes to.
func (s *BackupService) drBackupDir() string {
	f, err := os.Open(drBackupConfFile)
	if err != nil {
		return drBackupDirDefault
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// backup.conf is `.`-sourced by the shell, so a value may be written as
		// `export BZ_BACKUP_LOCAL_DIR=…` and may carry a trailing ` # comment`.
		// Handle both so a customized directory isn't silently ignored (which
		// would list/download/delete from the wrong place).
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		if v, ok := strings.CutPrefix(line, "BZ_BACKUP_LOCAL_DIR="); ok {
			if i := strings.Index(v, " #"); i >= 0 {
				v = v[:i]
			}
			v = strings.Trim(strings.TrimSpace(v), `"'`)
			if v != "" {
				return v
			}
		}
	}
	return drBackupDirDefault
}

// ListDRBackups returns the DR bundles on local disk, newest first. Only the
// archive bundles are listed; the .manifest.json / .sha256 sidecars stay hidden
// (they travel with their bundle and are cleaned up alongside it on delete).
func (s *BackupService) ListDRBackups() ([]DRBackup, error) { return scanDRBackups(s.drBackupDir()) }

// scanDRBackups is the dir-parameterized core of ListDRBackups (testable without
// touching /etc or the real backup directory).
func scanDRBackups(dir string) ([]DRBackup, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []DRBackup{}, nil
		}
		return nil, err
	}
	out := []DRBackup{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, drBackupPrefix) {
			continue
		}
		if strings.HasSuffix(name, ".manifest.json") || strings.HasSuffix(name, ".sha256") {
			continue
		}
		if !strings.HasSuffix(name, ".tar.gz") && !strings.HasSuffix(name, ".tar.gz.enc") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, DRBackup{
			Name:       name,
			Size:       info.Size(),
			SizeHuman:  drHumanSize(info.Size()),
			ModifiedAt: info.ModTime(),
			Encrypted:  strings.HasSuffix(name, ".enc"),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModifiedAt.After(out[j].ModifiedAt) })
	return out, nil
}

// validDRName is the hard gate on every path-taking DR operation: the name must
// be a bare DR bundle filename — correct prefix + archive suffix, no path
// separators, no "..". This blocks directory traversal via a crafted `name`.
func validDRName(name string) bool {
	if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return false
	}
	if !strings.HasPrefix(name, drBackupPrefix) {
		return false
	}
	return strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tar.gz.enc")
}

// DRBackupPath validates name and returns its absolute path if the bundle
// exists. Used by the download handler.
func (s *BackupService) DRBackupPath(name string) (string, error) {
	return drBundlePath(s.drBackupDir(), name)
}

func drBundlePath(dir, name string) (string, error) {
	if !validDRName(name) {
		return "", fmt.Errorf("invalid backup name")
	}
	p := filepath.Join(dir, name)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("backup not found")
	}
	return p, nil
}

// DeleteDRBackup removes a DR bundle and its manifest/sha256 sidecars. The
// name is validated first so a caller can never delete outside the DR directory.
func (s *BackupService) DeleteDRBackup(name string) error {
	return deleteDRBundle(s.drBackupDir(), name)
}

func deleteDRBundle(dir, name string) error {
	if !validDRName(name) {
		return fmt.Errorf("invalid backup name")
	}
	p := filepath.Join(dir, name)
	if _, err := os.Stat(p); err != nil {
		return fmt.Errorf("backup not found")
	}
	if err := os.Remove(p); err != nil {
		return err
	}
	// Sidecars are named <bundle>.manifest.json and <bundle>.sha256 — best-effort.
	_ = os.Remove(p + ".manifest.json")
	_ = os.Remove(p + ".sha256")
	return nil
}

// drHumanSize renders a byte count as a compact human string (e.g. "30.7 GB").
func drHumanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
