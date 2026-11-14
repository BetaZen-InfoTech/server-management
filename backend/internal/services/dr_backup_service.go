package services

import (
	"bufio"
	"fmt"
	"io"
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
	// drWorkDirDefault mirrors WORK_ROOT in scripts/bzpanel-backup.sh — the
	// staging dir where a bundle is built before it is encrypted/moved into
	// LOCAL_DIR. A failed/partial run leaves its raw bundle here, where the
	// retention sweep (which only scans LOCAL_DIR) never reaches it, so it
	// silently fills the disk. We surface these as "incomplete" so the owner
	// can delete them from the WHM Backups page.
	drWorkDirDefault = "/var/backups/bzpanel/work"
)

// DRBackup is one whole-server bundle on local disk.
type DRBackup struct {
	Name       string    `json:"name"`
	Size       int64     `json:"size"`
	SizeHuman  string    `json:"size_human"`
	ModifiedAt time.Time `json:"modified_at"`
	Encrypted  bool      `json:"encrypted"`
	// Incomplete is true for a bundle still sitting in the work/ staging dir —
	// i.e. left behind by a backup run that failed before encrypt/move. These
	// are safe to delete (a successful run writes the real bundle to LOCAL_DIR).
	Incomplete bool `json:"incomplete"`
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
	if dir := parseBZBackupVar(f, "BZ_BACKUP_LOCAL_DIR"); dir != "" {
		return dir
	}
	return drBackupDirDefault
}

// drWorkDir resolves the backup staging dir (BZ_BACKUP_WORK_DIR from
// backup.conf, else the default) — the same resolution scripts/bzpanel-backup.sh
// uses for WORK_ROOT — so the panel can surface the incomplete bundles a failed
// run orphaned there.
func (s *BackupService) drWorkDir() string {
	f, err := os.Open(drBackupConfFile)
	if err != nil {
		return drWorkDirDefault
	}
	defer f.Close()
	if dir := parseBZBackupVar(f, "BZ_BACKUP_WORK_DIR"); dir != "" {
		return dir
	}
	return drWorkDirDefault
}

// parseBZBackupLocalDir scans a shell-sourced backup.conf for BZ_BACKUP_LOCAL_DIR
// and returns its value ("" when absent). Kept as a thin wrapper over the generic
// parseBZBackupVar so the existing callers/tests stay stable.
func parseBZBackupLocalDir(r io.Reader) string { return parseBZBackupVar(r, "BZ_BACKUP_LOCAL_DIR") }

// parseBZBackupVar scans a shell-sourced backup.conf for the given key and
// returns its value ("" when absent). It tolerates the forms that are valid in
// the `.`-sourced file — an `export ` prefix, surrounding quotes, and a trailing
// ` # comment` — and honours last-assignment-wins like the shell would, so a
// customized value is never silently ignored (which would make the WHM Backups
// page manage the wrong directory).
func parseBZBackupVar(r io.Reader, key string) string {
	result := ""
	prefix := key + "="
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		if v, ok := strings.CutPrefix(line, prefix); ok {
			if i := strings.Index(v, " #"); i >= 0 {
				v = v[:i]
			}
			if v = strings.Trim(strings.TrimSpace(v), `"'`); v != "" {
				result = v
			}
		}
	}
	return result
}

// ListDRBackups returns the DR bundles on local disk, newest first. Only the
// archive bundles are listed; the .manifest.json / .sha256 sidecars stay hidden
// (they travel with their bundle and are cleaned up alongside it on delete).
// Bundles found in the work/ staging dir are flagged Incomplete so the owner can
// tell a real backup from a failed-run leftover (and clear the latter, which is
// what silently fills the disk).
func (s *BackupService) ListDRBackups() ([]DRBackup, error) {
	return scanDRBackupsMulti(s.drBackupDir(), s.drWorkDir())
}

// scanDRBackupsMulti merges the completed bundles in completeDir with the
// incomplete ones left in workDir (deduped by name — a completed bundle always
// wins over a same-named staging leftover), newest first.
func scanDRBackupsMulti(completeDir, workDir string) ([]DRBackup, error) {
	out, err := scanDRBackups(completeDir)
	if err != nil {
		return nil, err
	}
	if workDir != "" && workDir != completeDir {
		seen := make(map[string]struct{}, len(out))
		for _, b := range out {
			seen[b.Name] = struct{}{}
		}
		if work, werr := scanDRBackups(workDir); werr == nil {
			for _, b := range work {
				if _, dup := seen[b.Name]; dup {
					continue
				}
				b.Incomplete = true
				out = append(out, b)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModifiedAt.After(out[j].ModifiedAt) })
	return out, nil
}

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
// exists in either the completed dir or the work/ staging dir. Used by the
// download handler.
func (s *BackupService) DRBackupPath(name string) (string, error) {
	return drBundlePathIn([]string{s.drBackupDir(), s.drWorkDir()}, name)
}

func drBundlePath(dir, name string) (string, error) {
	return drBundlePathIn([]string{dir}, name)
}

// drBundlePathIn validates name once (traversal gate) then returns the first of
// dirs that actually holds the bundle. Joining a validated bare filename with a
// known-safe dir keeps the no-traversal guarantee.
func drBundlePathIn(dirs []string, name string) (string, error) {
	if !validDRName(name) {
		return "", fmt.Errorf("invalid backup name")
	}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("backup not found")
}

// DeleteDRBackup removes a DR bundle and its manifest/sha256 sidecars from
// whichever of the completed / work staging dirs holds it. The name is validated
// first so a caller can never delete outside those directories.
func (s *BackupService) DeleteDRBackup(name string) error {
	return deleteDRBundleIn([]string{s.drBackupDir(), s.drWorkDir()}, name)
}

func deleteDRBundle(dir, name string) error {
	return deleteDRBundleIn([]string{dir}, name)
}

func deleteDRBundleIn(dirs []string, name string) error {
	if !validDRName(name) {
		return fmt.Errorf("invalid backup name")
	}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if err := os.Remove(p); err != nil {
			return err
		}
		// Sidecars are named <bundle>.manifest.json and <bundle>.sha256 — best-effort.
		_ = os.Remove(p + ".manifest.json")
		_ = os.Remove(p + ".sha256")
		return nil
	}
	return fmt.Errorf("backup not found")
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
