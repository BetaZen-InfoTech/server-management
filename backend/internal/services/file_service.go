package services

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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

// validatePath ensures the resolved path stays within allowed directories.
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
	resolved := filepath.Clean(filepath.Join(base, path))
	if !strings.HasPrefix(resolved, base) {
		return "", fmt.Errorf("access denied: path traversal detected")
	}
	return resolved, nil
}

func (s *FileService) ListDirectory(ctx context.Context, user, path string) ([]map[string]interface{}, error) {
	if err := s.assertTenantOwnsUser(ctx, user); err != nil { return nil, err }
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
	if err := s.assertTenantOwnsUser(ctx, user); err != nil { return nil, err }
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
	if err := s.assertTenantOwnsUser(ctx, user); err != nil { return err }
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
	if err := s.assertTenantOwnsUser(ctx, user); err != nil { return err }
	resolvedPath, err := validatePath(user, path)
	if err != nil {
		return err
	}

	if err := os.WriteFile(resolvedPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to edit file: %w", err)
	}
	return nil
}

func (s *FileService) DeleteFile(ctx context.Context, user, path string) error {
	if err := s.assertTenantOwnsUser(ctx, user); err != nil { return err }
	resolvedPath, err := validatePath(user, path)
	if err != nil {
		return err
	}

	// Safety check: don't delete root-level directories
	if resolvedPath == "/" || resolvedPath == "/home" || resolvedPath == "/etc" || resolvedPath == "/var" {
		return fmt.Errorf("cannot delete system directories")
	}

	// Use rm -rf via RunCommand to handle symlinks, permission issues, and special chars
	if _, err := agent.RunCommand(ctx, "rm", "-rf", resolvedPath); err != nil {
		return fmt.Errorf("failed to delete: %w", err)
	}
	return nil
}

func (s *FileService) Upload(ctx context.Context, user, path string, files []*multipart.FileHeader) error {
	if err := s.assertTenantOwnsUser(ctx, user); err != nil { return err }
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

		if user != "" && user != "root" {
			agent.RunCommand(ctx, "chown", user+":"+user, targetPath)
		}
	}
	return nil
}

func (s *FileService) Mkdir(ctx context.Context, user, path string) error {
	if err := s.assertTenantOwnsUser(ctx, user); err != nil { return err }
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
	if err := s.assertTenantOwnsUser(ctx, user); err != nil { return err }
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
	if err := s.assertTenantOwnsUser(ctx, user); err != nil { return err }
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
	if err := s.assertTenantOwnsUser(ctx, user); err != nil { return nil, err }
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
	if err := s.assertTenantOwnsUser(ctx, user); err != nil { return "", err }
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
	if err := s.assertTenantOwnsUser(ctx, user); err != nil { return err }
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
	if err := s.assertTenantOwnsUser(ctx, user); err != nil { return err }
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
		args := append([]string{"-r", outputPath}, resolvedPaths...)
		_, err = agent.RunCommand(ctx, "zip", args...)
	default: // tar.gz
		args := append([]string{"-czf", outputPath}, resolvedPaths...)
		_, err = agent.RunCommand(ctx, "tar", args...)
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

	if strings.HasSuffix(archivePath, ".zip") {
		_, err = agent.RunCommand(ctx, "unzip", "-o", archivePath, "-d", destPath)
	} else {
		_, err = agent.RunCommand(ctx, "tar", "-xzf", archivePath, "-C", destPath)
	}
	if err != nil {
		return fmt.Errorf("extraction failed: %w", err)
	}

	if user != "" && user != "root" {
		agent.RunCommand(ctx, "chown", "-R", user+":"+user, destPath)
	}
	return nil
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
