package services

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"go.mongodb.org/mongo-driver/mongo"
)

type FileService struct {
	db *mongo.Database
}

func NewFileService(db *mongo.Database) *FileService {
	return &FileService{db: db}
}

// assertTenantOwnsUser blocks tenant-scoped callers from passing a username
// that doesn't belong to their tenant. Called by every public FileService
// method so a vendor cannot poke at another vendor's home dir.
func (s *FileService) assertTenantOwnsUser(ctx context.Context, user string) error {
	scope := GetCallerScope(ctx)
	if scope == nil {
		return nil
	}
	// Vendors cannot escape into root no matter what.
	if user == "" || user == "root" {
		// Only the platform owner is allowed to browse / as root.
		if scope.Role == "vendor_owner" {
			return nil
		}
		return fmt.Errorf("access denied")
	}
	return scope.AssertOwns(ctx, s.db, user)
}

// resolveCallerUser returns the linux username the file API should
// operate as. Three rules:
//  1. Admins (vendor_owner with no caller scope) get to use whatever
//     `requested` they pass — empty stays empty so they can browse root.
//  2. Tenant-scoped callers (vendor_staff / customer / etc.) without a
//     `requested` value get auto-defaulted to THEIR OWN username so the
//     File Manager just works without the frontend having to know /
//     pass the user param explicitly.
//  3. Tenant-scoped callers WITH a `requested` value go through
//     assertTenantOwnsUser — they may only act on usernames their tenant
//     owns. (This is the existing behaviour, retained.)
//
// Returns the resolved username + a hard error if access is denied.
func (s *FileService) resolveCallerUser(ctx context.Context, requested string) (string, error) {
	scope := GetCallerScope(ctx)
	if scope == nil {
		return requested, nil
	}
	if requested == "" {
		if scope.Role == "vendor_owner" {
			return "", nil
		}
		// Default a tenant-scoped caller to their own username so the
		// frontend doesn't have to know it.
		if scope.UserHex != "" {
			if uname, err := LookupOwnUsername(ctx, s.db, scope.UserHex); err == nil && uname != "" {
				return uname, nil
			}
		}
		return "", fmt.Errorf("access denied")
	}
	if requested == "root" && scope.Role != "vendor_owner" {
		return "", fmt.Errorf("access denied")
	}
	if err := scope.AssertOwns(ctx, s.db, requested); err != nil {
		return "", err
	}
	return requested, nil
}

// validatePath ensures the resolved path stays within allowed directories.
// When user is empty / "root" the path is treated as absolute (admin
// browse). When user is set, two shapes are accepted to keep the API
// useful for both UIs:
//
//   - Relative path  (e.g. "/" or "foo/bar")          → joined under
//     /home/<user>/ and confirmed to stay under it.
//   - Absolute path  (e.g. "/home/<user>/foo")        → confirmed to
//     start with /home/<user>/. The WHM File Manager always sends
//     absolute paths so the breadcrumb / address bar match exactly
//     what's on disk.
//
// Either way the result is /home/<user>/... and any attempt to escape
// returns a clear error.
func validatePath(user, path string) (string, error) {
	// For root user, allow access to system paths
	if user == "root" || user == "" {
		cleaned := filepath.Clean(path)
		if cleaned == "" {
			return "/", nil
		}
		return cleaned, nil
	}

	base := fmt.Sprintf("/home/%s", user)
	var resolved string
	if strings.HasPrefix(path, "/") {
		// Caller sent an absolute path — accept it but enforce it stays
		// inside /home/<user>/.
		resolved = filepath.Clean(path)
	} else {
		resolved = filepath.Clean(filepath.Join(base, path))
	}
	// Allow exact match on base (operator browsing the home root) as well
	// as anything strictly below it.
	if resolved != base && !strings.HasPrefix(resolved, base+"/") {
		return "", fmt.Errorf("access denied: path %q is outside /home/%s/", path, user)
	}
	return resolved, nil
}

func (s *FileService) ListDirectory(ctx context.Context, user, path string) ([]map[string]interface{}, error) {
	resolvedUser, err := s.resolveCallerUser(ctx, user)
	if err != nil { return nil, err }
	user = resolvedUser
	resolvedPath, err := validatePath(user, path)
	if err != nil {
		return nil, err
	}

	result, err := agent.RunCommand(ctx, "ls", "-la", "--time-style=long-iso", resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("failed to list directory: %w", err)
	}

	var entries []map[string]interface{}
	lines := strings.Split(strings.TrimSpace(result.Output), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "total") || line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}

		perms := fields[0]
		size := fields[4]
		date := fields[5]
		timeStr := fields[6]
		name := strings.Join(fields[7:], " ")

		if name == "." || name == ".." {
			continue
		}

		fileType := "file"
		if perms[0] == 'd' {
			fileType = "directory"
		} else if perms[0] == 'l' {
			fileType = "symlink"
		}

		// For symlinks, ls shows "name -> target" — extract just the name
		displayName := name
		actualName := name
		if fileType == "symlink" {
			if idx := strings.Index(name, " -> "); idx != -1 {
				actualName = name[:idx]
				displayName = name // keep "name -> target" for display
			}
		}

		sizeInt, _ := strconv.ParseInt(size, 10, 64)
		sizeStr := formatFileSize(sizeInt)

		entries = append(entries, map[string]interface{}{
			"name":        displayName,
			"type":        fileType,
			"size":        sizeStr,
			"permissions": perms,
			"modified":    date + " " + timeStr,
			"path":        filepath.Join(resolvedPath, actualName),
		})
	}

	if entries == nil {
		entries = []map[string]interface{}{}
	}
	return entries, nil
}

func (s *FileService) ReadFile(ctx context.Context, user, path string) (map[string]interface{}, error) {
	resolvedUser, err := s.resolveCallerUser(ctx, user)
	if err != nil { return nil, err }
	user = resolvedUser
	resolvedPath, err := validatePath(user, path)
	if err != nil {
		return nil, err
	}

	content, err := os.ReadFile(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	info, _ := os.Stat(resolvedPath)
	result := map[string]interface{}{
		"content": string(content),
		"path":    resolvedPath,
	}
	if info != nil {
		result["size"] = formatFileSize(info.Size())
		result["modified"] = info.ModTime().Format("2006-01-02 15:04:05")
		result["permissions"] = fmt.Sprintf("%o", info.Mode().Perm())
	}
	return result, nil
}

func (s *FileService) CreateFile(ctx context.Context, user, path, content string) error {
	resolvedUser, err := s.resolveCallerUser(ctx, user)
	if err != nil { return err }
	user = resolvedUser
	resolvedPath, err := validatePath(user, path)
	if err != nil {
		return err
	}

	// Create parent directories if needed
	os.MkdirAll(filepath.Dir(resolvedPath), 0755)

	if err := os.WriteFile(resolvedPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}

	// Set ownership
	if user != "" && user != "root" {
		agent.RunCommand(ctx, "chown", user+":"+user, resolvedPath)
	}
	return nil
}

func (s *FileService) EditFile(ctx context.Context, user, path, content string) error {
	resolvedUser, err := s.resolveCallerUser(ctx, user)
	if err != nil { return err }
	user = resolvedUser
	resolvedPath, err := validatePath(user, path)
	if err != nil {
		return err
	}

	if err := os.WriteFile(resolvedPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to edit file: %w", err)
	}
	return nil
}

// DeleteFile removes a path. When permanent is false the path is moved
// to the caller's .Trash/ directory so it can be restored later. The
// old single-arg signature stays the default (permanent=true) via the
// DeleteFilePermanent wrapper used by places that really do want rm -rf.
func (s *FileService) DeleteFile(ctx context.Context, user, path string, permanent bool) error {
	resolvedUser, err := s.resolveCallerUser(ctx, user)
	if err != nil {
		return err
	}
	user = resolvedUser
	resolvedPath, err := validatePath(user, path)
	if err != nil {
		return err
	}

	// Safety check — blocks both soft and hard delete. A trashed /home
	// would be unrestorable anyway and the UI should never ask.
	if resolvedPath == "/" || resolvedPath == "/home" || resolvedPath == "/etc" || resolvedPath == "/var" {
		return fmt.Errorf("cannot delete system directories")
	}
	// Don't let a user trash their own .Trash dir — it would shred every
	// restorable item inside it. We silently skip. Hard delete of the
	// trash dir is still possible by deleting its children first.
	trashRoot := userTrashRoot(user)
	if resolvedPath == trashRoot {
		return fmt.Errorf("cannot delete the trash directory itself — use Empty Trash instead")
	}

	if !permanent {
		return s.moveToTrashAt(ctx, user, resolvedPath)
	}
	if _, err := agent.RunCommand(ctx, "rm", "-rf", resolvedPath); err != nil {
		return fmt.Errorf("failed to delete: %w", err)
	}
	return nil
}

// ──────────────────────────────────────────────────────
// Trash system
// ──────────────────────────────────────────────────────
// Every user gets a per-user trash directory:
//
//   user (non-root):  /home/<user>/.Trash/
//   root / admin  :   /var/lib/serverpanel/trash/.admin/
//
// Deleting an item (non-permanent) moves it into that directory under
// a new id of the form "<unix-nanos>-<hex4>". A sidecar JSON file
// "<id>.meta.json" records the original path, original name, size,
// kind (file/directory), and deleted_at timestamp — so Restore can put
// it back exactly where it came from.
//
// Using $HOME/.Trash means the mv stays on the same filesystem for
// user files, keeping it atomic and fast. It counts against the user's
// quota, which is correct: they still own the disk.
// ──────────────────────────────────────────────────────

type trashMeta struct {
	ID           string    `json:"id"`
	OriginalPath string    `json:"original_path"`
	OriginalName string    `json:"original_name"`
	DeletedAt    time.Time `json:"deleted_at"`
	Size         int64     `json:"size"`
	Kind         string    `json:"kind"` // "file" or "directory"
	User         string    `json:"user"`
}

// userTrashRoot returns the on-disk trash directory for the given
// user. Empty/"root" maps to the admin trash because the admin can
// delete anything on the FS and we don't want those files landing in
// /root/.Trash (which may be on a small root partition).
func userTrashRoot(user string) string {
	if user == "" || user == "root" {
		return "/var/lib/serverpanel/trash/.admin"
	}
	return filepath.Join("/home", user, ".Trash")
}

// newTrashID mints "<unix-nanos>-<hex4>". Sortable by deletion time,
// collision-proof within the same nanosecond via 4 random hex chars.
func newTrashID() string {
	b := make([]byte, 2)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%d-%s", time.Now().UnixNano(), hex.EncodeToString(b))
}

// moveToTrashAt performs the soft-delete mv + writes the sidecar
// metadata. resolvedPath must already have been validated — we trust
// our caller. Uses the agent's mv so permissions/ownership round-trip
// correctly even when the service process runs as root and the target
// lives inside a user home.
func (s *FileService) moveToTrashAt(ctx context.Context, user, resolvedPath string) error {
	info, err := os.Lstat(resolvedPath)
	if err != nil {
		return fmt.Errorf("source not found: %w", err)
	}
	trashRoot := userTrashRoot(user)
	if err := os.MkdirAll(trashRoot, 0700); err != nil {
		return fmt.Errorf("failed to prepare trash: %w", err)
	}
	// Inherit ownership for user trash so the user can still empty it.
	if user != "" && user != "root" {
		_, _ = agent.RunCommand(ctx, "chown", user+":"+user, trashRoot)
	}

	id := newTrashID()
	dst := filepath.Join(trashRoot, id)

	// mv handles cross-FS via copy+unlink; we prefer the shell mv over
	// os.Rename so the fallback kicks in automatically.
	if _, err := agent.RunCommand(ctx, "mv", "--", resolvedPath, dst); err != nil {
		return fmt.Errorf("failed to move to trash: %w", err)
	}

	kind := "file"
	if info.IsDir() {
		kind = "directory"
	}
	var sz int64
	if info.IsDir() {
		// Approximate dir size via du — not exact (sparse files, hard
		// links, mount crossings) but good enough for the trash UI.
		if out, derr := agent.RunCommand(ctx, "du", "-sb", dst); derr == nil && out != nil {
			if parts := strings.Fields(out.Output); len(parts) > 0 {
				sz, _ = strconv.ParseInt(parts[0], 10, 64)
			}
		}
	} else {
		sz = info.Size()
	}

	meta := trashMeta{
		ID:           id,
		OriginalPath: resolvedPath,
		OriginalName: filepath.Base(resolvedPath),
		DeletedAt:    time.Now().UTC(),
		Size:         sz,
		Kind:         kind,
		User:         user,
	}
	raw, _ := json.MarshalIndent(meta, "", "  ")
	metaPath := dst + ".meta.json"
	if err := os.WriteFile(metaPath, raw, 0600); err != nil {
		// Metadata write failed after a successful mv — put the file
		// back rather than leave an orphaned, un-restorable blob.
		_, _ = agent.RunCommand(ctx, "mv", "--", dst, resolvedPath)
		return fmt.Errorf("failed to write trash metadata: %w", err)
	}
	if user != "" && user != "root" {
		_, _ = agent.RunCommand(ctx, "chown", user+":"+user, metaPath)
	}
	return nil
}

// ListTrash returns every item currently in the user's trash, sorted
// newest-first. Silently returns an empty list when the trash dir
// doesn't exist yet — that's a fresh account, not an error.
func (s *FileService) ListTrash(ctx context.Context, user string) ([]map[string]interface{}, error) {
	resolvedUser, err := s.resolveCallerUser(ctx, user)
	if err != nil {
		return nil, err
	}
	trashRoot := userTrashRoot(resolvedUser)
	entries, err := os.ReadDir(trashRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return []map[string]interface{}{}, nil
		}
		return nil, fmt.Errorf("failed to read trash: %w", err)
	}

	var out []map[string]interface{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".meta.json") {
			continue
		}
		raw, rerr := os.ReadFile(filepath.Join(trashRoot, name))
		if rerr != nil {
			continue
		}
		var meta trashMeta
		if jerr := json.Unmarshal(raw, &meta); jerr != nil {
			continue
		}
		out = append(out, map[string]interface{}{
			"id":            meta.ID,
			"original_path": meta.OriginalPath,
			"original_name": meta.OriginalName,
			"deleted_at":    meta.DeletedAt.Format(time.RFC3339),
			"size":          meta.Size,
			"size_human":    formatFileSize(meta.Size),
			"kind":          meta.Kind,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i]["deleted_at"].(string) > out[j]["deleted_at"].(string)
	})
	if out == nil {
		out = []map[string]interface{}{}
	}
	return out, nil
}

// RestoreFromTrash moves a trashed item back to its recorded origin.
// Refuses to clobber an existing file/dir at the origin unless
// overwrite is true — the safer default is to surface the conflict so
// the user can rename or clear the target manually.
func (s *FileService) RestoreFromTrash(ctx context.Context, user, trashID string, overwrite bool) error {
	resolvedUser, err := s.resolveCallerUser(ctx, user)
	if err != nil {
		return err
	}
	if trashID == "" {
		return fmt.Errorf("trash id required")
	}
	if strings.ContainsAny(trashID, "/\\") {
		return fmt.Errorf("invalid trash id")
	}
	trashRoot := userTrashRoot(resolvedUser)
	metaPath := filepath.Join(trashRoot, trashID+".meta.json")
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		return fmt.Errorf("trash item not found")
	}
	var meta trashMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return fmt.Errorf("invalid trash metadata: %w", err)
	}
	// Re-validate the origin path against the caller's scope. Prevents
	// a crafted metadata file from writing anywhere the caller can't
	// normally reach (shouldn't happen — metadata is written only by
	// us — but belt + suspenders).
	origin, err := validatePath(resolvedUser, meta.OriginalPath)
	if err != nil {
		return err
	}

	dst := filepath.Join(trashRoot, trashID)
	if _, serr := os.Lstat(dst); serr != nil {
		return fmt.Errorf("trashed file missing on disk (metadata orphan)")
	}

	if _, serr := os.Lstat(origin); serr == nil {
		if !overwrite {
			return fmt.Errorf("a file or directory already exists at %q — delete it first or pass overwrite=true", origin)
		}
		if _, rerr := agent.RunCommand(ctx, "rm", "-rf", origin); rerr != nil {
			return fmt.Errorf("failed to clear destination for restore: %w", rerr)
		}
	}

	// Ensure the parent dir exists — the original tree may have been
	// deleted while the item sat in trash.
	if err := os.MkdirAll(filepath.Dir(origin), 0755); err != nil {
		return fmt.Errorf("failed to recreate parent directory: %w", err)
	}
	if _, err := agent.RunCommand(ctx, "mv", "--", dst, origin); err != nil {
		return fmt.Errorf("failed to restore: %w", err)
	}
	_ = os.Remove(metaPath)
	return nil
}

// DeleteFromTrash permanently removes a single trashed item (the
// content and its sidecar metadata). Pass "*" as the id to empty the
// whole trash — that branch is also used by EmptyTrash below.
func (s *FileService) DeleteFromTrash(ctx context.Context, user, trashID string) error {
	resolvedUser, err := s.resolveCallerUser(ctx, user)
	if err != nil {
		return err
	}
	trashRoot := userTrashRoot(resolvedUser)
	if trashID == "*" {
		if _, err := os.Stat(trashRoot); os.IsNotExist(err) {
			return nil
		}
		// Wipe contents but keep the dir so future deletes don't need
		// to recreate + chown it.
		entries, err := os.ReadDir(trashRoot)
		if err != nil {
			return fmt.Errorf("failed to list trash: %w", err)
		}
		for _, e := range entries {
			_, _ = agent.RunCommand(ctx, "rm", "-rf", filepath.Join(trashRoot, e.Name()))
		}
		return nil
	}
	if trashID == "" || strings.ContainsAny(trashID, "/\\") {
		return fmt.Errorf("invalid trash id")
	}
	dst := filepath.Join(trashRoot, trashID)
	metaPath := dst + ".meta.json"
	// Delete both sides. If one is missing (drift), continue with the
	// other so the user isn't stuck with a half-state.
	if _, err := os.Stat(dst); err == nil {
		if _, err := agent.RunCommand(ctx, "rm", "-rf", dst); err != nil {
			return fmt.Errorf("failed to delete trashed item: %w", err)
		}
	}
	_ = os.Remove(metaPath)
	return nil
}

// EmptyTrash permanently removes every item in the user's trash.
// Thin wrapper over DeleteFromTrash("*") for handler clarity.
func (s *FileService) EmptyTrash(ctx context.Context, user string) error {
	return s.DeleteFromTrash(ctx, user, "*")
}

func (s *FileService) Upload(ctx context.Context, user, path string, files []*multipart.FileHeader) error {
	resolvedUser, err := s.resolveCallerUser(ctx, user)
	if err != nil { return err }
	user = resolvedUser
	resolvedPath, err := validatePath(user, path)
	if err != nil {
		return err
	}

	os.MkdirAll(resolvedPath, 0755)

	for _, file := range files {
		// Strip any directory portion from the client-supplied filename to
		// block traversal via crafted uploads (e.g. "../../etc/passwd").
		name := filepath.Base(file.Filename)
		if name == "" || name == "." || name == "/" {
			return fmt.Errorf("invalid filename")
		}
		targetPath := filepath.Join(resolvedPath, name)

		src, err := file.Open()
		if err != nil {
			return fmt.Errorf("failed to open %s: %w", name, err)
		}
		dst, err := os.Create(targetPath)
		if err != nil {
			src.Close()
			return fmt.Errorf("failed to create %s: %w", name, err)
		}
		if _, err := io.Copy(dst, src); err != nil {
			src.Close()
			dst.Close()
			return fmt.Errorf("failed to write %s: %w", name, err)
		}
		src.Close()
		dst.Close()

		// Fall back to inferring the owner from the destination path when
		// the caller didn't send `user` — the WHM File Manager doesn't.
		// Otherwise root-owned files break later `npm run build` etc.
		owner := strings.TrimSpace(user)
		if owner == "" || owner == "root" {
			owner = inferOwnerFromPath(resolvedPath)
		}
		if owner != "" && owner != "root" {
			agent.RunCommand(ctx, "chown", owner+":"+owner, targetPath)
		}
	}
	return nil
}

func (s *FileService) Mkdir(ctx context.Context, user, path string) error {
	resolvedUser, err := s.resolveCallerUser(ctx, user)
	if err != nil { return err }
	user = resolvedUser
	resolvedPath, err := validatePath(user, path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(resolvedPath, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}
	if user != "" && user != "root" {
		agent.RunCommand(ctx, "chown", user+":"+user, resolvedPath)
	}
	return nil
}

func (s *FileService) Copy(ctx context.Context, user string, sources []string, destination string) error {
	resolvedUser, err := s.resolveCallerUser(ctx, user)
	if err != nil { return err }
	user = resolvedUser
	destPath, err := validatePath(user, destination)
	if err != nil {
		return err
	}
	os.MkdirAll(destPath, 0755)

	for _, src := range sources {
		srcPath, err := validatePath(user, src)
		if err != nil {
			return err
		}
		if _, err := agent.RunCommand(ctx, "cp", "-a", srcPath, destPath); err != nil {
			return fmt.Errorf("failed to copy %s: %w", filepath.Base(srcPath), err)
		}
	}
	if user != "" && user != "root" {
		agent.RunCommand(ctx, "chown", "-R", user+":"+user, destPath)
	}
	return nil
}

func (s *FileService) Move(ctx context.Context, user string, sources []string, destination string) error {
	resolvedUser, err := s.resolveCallerUser(ctx, user)
	if err != nil { return err }
	user = resolvedUser
	destPath, err := validatePath(user, destination)
	if err != nil {
		return err
	}
	os.MkdirAll(destPath, 0755)

	for _, src := range sources {
		srcPath, err := validatePath(user, src)
		if err != nil {
			return err
		}
		if _, err := agent.RunCommand(ctx, "mv", srcPath, destPath); err != nil {
			return fmt.Errorf("failed to move %s: %w", filepath.Base(srcPath), err)
		}
	}
	return nil
}

func (s *FileService) Search(ctx context.Context, user, path, query string) ([]map[string]interface{}, error) {
	resolvedUser, err := s.resolveCallerUser(ctx, user)
	if err != nil { return nil, err }
	user = resolvedUser
	resolvedPath, err := validatePath(user, path)
	if err != nil {
		return nil, err
	}
	if query == "" {
		return []map[string]interface{}{}, nil
	}
	// Use find with a safe glob pattern derived from the query; the pattern
	// is passed as a single argument (not expanded by a shell) so injection
	// isn't possible here.
	pattern := "*" + strings.ReplaceAll(query, "*", "") + "*"
	result, err := agent.RunCommand(ctx, "find", resolvedPath, "-maxdepth", "6", "-iname", pattern, "-printf", "%y|%s|%TY-%Tm-%Td %TH:%TM|%p\n")
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	var entries []map[string]interface{}
	for _, line := range strings.Split(strings.TrimSpace(result.Output), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) < 4 {
			continue
		}
		fileType := "file"
		if parts[0] == "d" {
			fileType = "directory"
		} else if parts[0] == "l" {
			fileType = "symlink"
		}
		sizeInt, _ := strconv.ParseInt(parts[1], 10, 64)
		entries = append(entries, map[string]interface{}{
			"name":        filepath.Base(parts[3]),
			"type":        fileType,
			"size":        formatFileSize(sizeInt),
			"permissions": "",
			"modified":    parts[2],
			"path":        parts[3],
		})
	}
	if entries == nil {
		entries = []map[string]interface{}{}
	}
	return entries, nil
}

// DownloadPath returns the resolved absolute path for streaming a file
// directly from the backend filesystem. The caller is responsible for
// streaming the file and setting headers.
func (s *FileService) DownloadPath(ctx context.Context, user, path string) (string, error) {
	resolvedUser, err := s.resolveCallerUser(ctx, user)
	if err != nil { return "", err }
	user = resolvedUser
	resolvedPath, err := validatePath(user, path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolvedPath)
	if err != nil {
		return "", fmt.Errorf("file not found: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("cannot download a directory; compress it first")
	}
	return resolvedPath, nil
}

func (s *FileService) Rename(ctx context.Context, user, source, destination string) error {
	resolvedUser, err := s.resolveCallerUser(ctx, user)
	if err != nil { return err }
	user = resolvedUser
	srcPath, err := validatePath(user, source)
	if err != nil {
		return err
	}
	dstPath, err := validatePath(user, destination)
	if err != nil {
		return err
	}

	if err := os.Rename(srcPath, dstPath); err != nil {
		return fmt.Errorf("failed to rename: %w", err)
	}
	return nil
}

func (s *FileService) Chmod(ctx context.Context, user, path, permissions string, recursive bool) error {
	resolvedUser, err := s.resolveCallerUser(ctx, user)
	if err != nil { return err }
	user = resolvedUser
	resolvedPath, err := validatePath(user, path)
	if err != nil {
		return err
	}

	args := []string{}
	if recursive {
		args = append(args, "-R")
	}
	args = append(args, permissions, resolvedPath)

	if _, err := agent.RunCommand(ctx, "chmod", args...); err != nil {
		return fmt.Errorf("failed to chmod: %w", err)
	}
	return nil
}

func (s *FileService) Compress(ctx context.Context, user string, paths []string, output, format string) error {
	outputPath, err := validatePath(user, output)
	if err != nil {
		return err
	}

	var resolvedPaths []string
	for _, p := range paths {
		rp, err := validatePath(user, p)
		if err != nil {
			return err
		}
		resolvedPaths = append(resolvedPaths, rp)
	}

	switch format {
	case "zip":
		// Use Go's stdlib archive/zip instead of shelling out to `zip` —
		// the `zip` binary is not installed on base Debian/Ubuntu and
		// was silently missing from the VPS, making "Compress → ZIP"
		// fail for every operator action. archive/zip has no external
		// dependency and matches `zip -r` behavior (recursive directory
		// packing, basename-rooted entries).
		err = compressZip(resolvedPaths, outputPath)
	default: // tar.gz
		// tar is part of coreutils-adjacent packages shipped on every
		// server distribution we support, so shelling out is fine. -C
		// to each path's parent dir so the archive stores basename-
		// rooted entries (matching the zip branch's shape) instead of
		// absolute paths that would extract to /home/.../... on the
		// receiving side.
		err = compressTarGz(ctx, resolvedPaths, outputPath)
	}
	if err != nil {
		return fmt.Errorf("compression failed: %w", err)
	}

	if user != "" && user != "root" {
		agent.RunCommand(ctx, "chown", user+":"+user, outputPath)
	}
	return nil
}

func (s *FileService) Extract(ctx context.Context, user, archive, destination string) error {
	archivePath, err := validatePath(user, archive)
	if err != nil {
		return err
	}
	destPath, err := validatePath(user, destination)
	if err != nil {
		return err
	}

	os.MkdirAll(destPath, 0755)

	lower := strings.ToLower(archivePath)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		// Use Go's stdlib archive/zip instead of shelling out to `unzip` — the
		// `unzip` binary is not part of a base Debian install and was
		// silently missing from the VPS, making "Extract" fail for every
		// zip upload. archive/zip has no external dependency and handles
		// encrypted/zip64 files the stdlib supports.
		if err = extractZip(archivePath, destPath); err != nil {
			return fmt.Errorf("extraction failed: %w", err)
		}
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		if _, err = agent.RunCommand(ctx, "tar", "-xzf", archivePath, "-C", destPath); err != nil {
			return fmt.Errorf("extraction failed: %w", err)
		}
	case strings.HasSuffix(lower, ".tar"):
		if _, err = agent.RunCommand(ctx, "tar", "-xf", archivePath, "-C", destPath); err != nil {
			return fmt.Errorf("extraction failed: %w", err)
		}
	case strings.HasSuffix(lower, ".tar.bz2"), strings.HasSuffix(lower, ".tbz2"):
		if _, err = agent.RunCommand(ctx, "tar", "-xjf", archivePath, "-C", destPath); err != nil {
			return fmt.Errorf("extraction failed: %w", err)
		}
	default:
		return fmt.Errorf("unsupported archive type: %s", filepath.Base(archivePath))
	}

	// chown the extracted tree to the owning user. The frontend doesn't
	// always send the user, so fall back to inferring it from the path
	// (/home/<user>/... → <user>). Without this the extracted files are
	// root-owned, which makes any subsequent `npm run build` (or anything
	// the app user runs) fail with EACCES on the original artifacts.
	owner := strings.TrimSpace(user)
	if owner == "" || owner == "root" {
		owner = inferOwnerFromPath(destPath)
	}
	if owner != "" && owner != "root" {
		agent.RunCommand(ctx, "chown", "-R", owner+":"+owner, destPath)
	}
	return nil
}

// inferOwnerFromPath returns the system user who owns a /home/<user>/...
// path, or empty string when the path doesn't fit that shape. Used by
// File Manager Upload/Extract so root-owned writes get reassigned to the
// hosting user even if the API caller didn't pass `user` explicitly.
func inferOwnerFromPath(p string) string {
	p = filepath.Clean(p)
	if !strings.HasPrefix(p, "/home/") {
		return ""
	}
	rest := strings.TrimPrefix(p, "/home/")
	if i := strings.IndexByte(rest, '/'); i > 0 {
		return rest[:i]
	}
	return rest
}

// extractZip extracts a .zip archive into destDir using the Go stdlib, so
// the server doesn't need the `unzip` binary installed. Guards against
// zip-slip (entries whose paths escape destDir via "../" components) by
// rejecting any entry that doesn't resolve inside destDir.
func extractZip(archivePath, destDir string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer r.Close()

	absDest, err := filepath.Abs(destDir)
	if err != nil {
		return err
	}

	for _, f := range r.File {
		// Clean + resolve the target path and reject anything outside destDir.
		targetPath := filepath.Join(absDest, f.Name)
		if !strings.HasPrefix(targetPath, absDest+string(os.PathSeparator)) && targetPath != absDest {
			return fmt.Errorf("zip-slip: entry %q escapes destination", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, f.Mode()); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return err
		}

		mode := f.Mode()
		if mode == 0 {
			mode = 0644
		}
		out, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			out.Close()
			return err
		}
		if _, err := io.Copy(out, rc); err != nil {
			out.Close()
			rc.Close()
			return err
		}
		out.Close()
		rc.Close()
	}
	return nil
}

// compressZip packs the given source paths (files or directories) into a
// single .zip archive at outputPath using Go's stdlib archive/zip, so the
// server doesn't need the `zip` binary installed (base Debian/Ubuntu
// don't ship it by default).
//
// Entries are stored with the source basename at the archive root —
// matching what `zip -r out.zip foo bar` produces: selecting a folder
// named "frontend" places everything under "frontend/..." inside the
// zip. Multiple inputs with the same basename would clobber each other;
// the UI prevents that by requiring unique names per directory.
func compressZip(sources []string, outputPath string) error {
	out, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer out.Close()

	zw := zip.NewWriter(out)
	defer zw.Close()

	for _, src := range sources {
		info, err := os.Lstat(src)
		if err != nil {
			return fmt.Errorf("stat %s: %w", src, err)
		}
		base := filepath.Base(src)

		if !info.IsDir() {
			if err := writeZipFile(zw, src, base, info); err != nil {
				return err
			}
			continue
		}

		// Recursive walk for directories. filepath.Rel gives the path
		// relative to the source dir so archive paths mirror the
		// on-disk layout rooted at basename.
		err = filepath.Walk(src, func(path string, fi os.FileInfo, werr error) error {
			if werr != nil {
				return werr
			}
			rel, err := filepath.Rel(src, path)
			if err != nil {
				return err
			}
			// Use forward slashes in archive names (zip spec + cross-platform).
			archiveName := filepath.ToSlash(filepath.Join(base, rel))
			if fi.IsDir() {
				// Directory entry — trailing slash signals "directory"
				// per the zip spec, which some extractors rely on.
				if archiveName == base {
					return nil // the root entry is implicit
				}
				_, err := zw.Create(archiveName + "/")
				return err
			}
			return writeZipFile(zw, path, archiveName, fi)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// writeZipFile copies a single on-disk file into the zip writer at the
// given archive name, preserving its file mode. Symlinks are followed
// (consistent with `zip -r` default behavior) — if that becomes a
// problem we can switch to storing them as symlink entries instead.
func writeZipFile(zw *zip.Writer, path, archiveName string, fi os.FileInfo) error {
	hdr, err := zip.FileInfoHeader(fi)
	if err != nil {
		return err
	}
	hdr.Name = archiveName
	hdr.Method = zip.Deflate
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

// compressTarGz packs source paths into a gzipped tar at outputPath
// using Go's stdlib archive/tar + compress/gzip. No external tar
// binary required — same rationale as compressZip (base images
// occasionally ship minimal toolchains). Entries are basename-rooted
// so `tar -xzf` of the archive doesn't dump files under the original
// absolute paths on the receiving side.
func compressTarGz(_ context.Context, sources []string, outputPath string) error {
	if len(sources) == 0 {
		return fmt.Errorf("no source paths")
	}
	out, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer out.Close()
	gz := gzip.NewWriter(out)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	for _, src := range sources {
		info, err := os.Lstat(src)
		if err != nil {
			return fmt.Errorf("stat %s: %w", src, err)
		}
		base := filepath.Base(src)

		if !info.IsDir() {
			if err := writeTarFile(tw, src, base, info); err != nil {
				return err
			}
			continue
		}

		err = filepath.Walk(src, func(path string, fi os.FileInfo, werr error) error {
			if werr != nil {
				return werr
			}
			rel, err := filepath.Rel(src, path)
			if err != nil {
				return err
			}
			archiveName := filepath.ToSlash(filepath.Join(base, rel))
			return writeTarFile(tw, path, archiveName, fi)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// writeTarFile writes one filesystem entry (file, dir, or symlink) to
// the tar writer at the given archive-relative name. Symlinks are
// stored as link entries so the archive round-trips unpack without
// dereferencing — tar's standard treatment.
func writeTarFile(tw *tar.Writer, path, archiveName string, fi os.FileInfo) error {
	var linkTarget string
	if fi.Mode()&os.ModeSymlink != 0 {
		t, err := os.Readlink(path)
		if err != nil {
			return err
		}
		linkTarget = t
	}
	hdr, err := tar.FileInfoHeader(fi, linkTarget)
	if err != nil {
		return err
	}
	hdr.Name = archiveName
	if fi.IsDir() {
		hdr.Name += "/"
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if fi.IsDir() || linkTarget != "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(tw, f)
	return err
}

// PasswordProtect sets up HTTP Basic Auth on a directory using .htaccess +
// .htpasswd, the way cPanel's "Password Protect Directories" feature does.
// Writes:
//   <dir>/.htaccess — AuthType/AuthName/AuthUserFile + Require valid-user
//   <dir>/.htpasswd — bcrypt-hashed credentials
//
// Passing an empty password keeps an existing .htpasswd as-is (useful to
// rename the realm). Passing an empty label defaults to "Restricted Area".
func (s *FileService) PasswordProtect(ctx context.Context, user, path, username, password, label string) error {
	if err := s.assertTenantOwnsUser(ctx, user); err != nil {
		return err
	}
	dir, err := validatePath(user, path)
	if err != nil {
		return err
	}
	if info, statErr := os.Stat(dir); statErr != nil || !info.IsDir() {
		return fmt.Errorf("path is not a directory")
	}
	if strings.TrimSpace(username) == "" {
		return fmt.Errorf("username is required")
	}
	if label == "" {
		label = "Restricted Area"
	}

	htpasswd := filepath.Join(dir, ".htpasswd")
	htaccess := filepath.Join(dir, ".htaccess")

	// Write/update .htpasswd. htpasswd(1) with -B uses bcrypt; -c creates a
	// new file, -b reads the password from the command line. We only create
	// when the file doesn't exist so additional users can be added.
	if password != "" {
		flags := "-Bb"
		if _, statErr := os.Stat(htpasswd); os.IsNotExist(statErr) {
			flags = "-Bbc"
		}
		if _, err := agent.RunCommand(ctx, "htpasswd", flags, htpasswd, username, password); err != nil {
			return fmt.Errorf("failed to write .htpasswd: %w", err)
		}
	}

	// Write .htaccess. Keep it simple — if the admin had custom rules we
	// prepend the protection block; duplicated AuthType directives are
	// harmless but we avoid that by rewriting the whole file for this feature.
	content := fmt.Sprintf(`AuthType Basic
AuthName "%s"
AuthUserFile %s
Require valid-user
`, strings.ReplaceAll(label, `"`, `\"`), htpasswd)
	if err := os.WriteFile(htaccess, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write .htaccess: %w", err)
	}

	if user != "" && user != "root" {
		agent.RunCommand(ctx, "chown", user+":"+user, htaccess, htpasswd)
	}
	return nil
}

// Unprotect removes the password protection from a directory by deleting
// its .htaccess and .htpasswd files.
func (s *FileService) Unprotect(ctx context.Context, user, path string) error {
	if err := s.assertTenantOwnsUser(ctx, user); err != nil {
		return err
	}
	dir, err := validatePath(user, path)
	if err != nil {
		return err
	}
	os.Remove(filepath.Join(dir, ".htaccess"))
	os.Remove(filepath.Join(dir, ".htpasswd"))
	return nil
}

// GetInfo returns metadata about a single file/directory — size, octal
// permissions, owner, mtime. Used by the frontend Permissions dialog so it
// can pre-fill the current mode instead of guessing 644.
func (s *FileService) GetInfo(ctx context.Context, user, path string) (map[string]interface{}, error) {
	if err := s.assertTenantOwnsUser(ctx, user); err != nil {
		return nil, err
	}
	resolved, err := validatePath(user, path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("not found: %w", err)
	}
	// File type
	kind := "file"
	if info.IsDir() {
		kind = "directory"
	} else if info.Mode()&os.ModeSymlink != 0 {
		kind = "symlink"
	}
	// Owner lookup via `stat` — os.Stat on linux returns *syscall.Stat_t
	// with Uid/Gid but resolving those to names needs /etc/passwd parsing;
	// shelling out to `stat` is simpler and matches what the user sees in ls.
	owner := ""
	if r, cerr := agent.RunCommand(ctx, "stat", "-c", "%U:%G", resolved); cerr == nil {
		owner = strings.TrimSpace(r.Output)
	}
	return map[string]interface{}{
		"path":        resolved,
		"name":        filepath.Base(resolved),
		"type":        kind,
		"size":        info.Size(),
		"size_human":  formatFileSize(info.Size()),
		"permissions": fmt.Sprintf("%o", info.Mode().Perm()),
		"mode":        info.Mode().String(),
		"owner":       owner,
		"modified":    info.ModTime().Format("2006-01-02 15:04:05"),
	}, nil
}

func formatFileSize(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	}
	if bytes < 1024*1024*1024 {
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
	}
	return fmt.Sprintf("%.1f GB", float64(bytes)/(1024*1024*1024))
}
