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

		sizeInt, _ := strconv.ParseInt(size, 10, 64)
		sizeStr := formatFileSize(sizeInt)

		entries = append(entries, map[string]interface{}{
			"name":        name,
			"type":        fileType,
			"size":        sizeStr,
			"permissions": perms,
			"modified":    date + " " + timeStr,
			"path":        filepath.Join(resolvedPath, name),
		})
	}

	if entries == nil {
		entries = []map[string]interface{}{}
	}
	return entries, nil
}

func (s *FileService) ReadFile(ctx context.Context, user, path string) (map[string]interface{}, error) {
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
	resolvedPath, err := validatePath(user, path)
	if err != nil {
		return err
	}

	// Safety check: don't delete root-level directories
	if resolvedPath == "/" || resolvedPath == "/home" || resolvedPath == "/etc" || resolvedPath == "/var" {
		return fmt.Errorf("cannot delete system directories")
	}

	if err := os.RemoveAll(resolvedPath); err != nil {
		return fmt.Errorf("failed to delete: %w", err)
	}
	return nil
}

func (s *FileService) Upload(ctx context.Context, user, path string, file *multipart.FileHeader) error {
	resolvedPath, err := validatePath(user, path)
	if err != nil {
		return err
	}

	targetPath := filepath.Join(resolvedPath, file.Filename)

	// Open uploaded file
	src, err := file.Open()
	if err != nil {
		return fmt.Errorf("failed to open uploaded file: %w", err)
	}
	defer src.Close()

	// Create target directories if needed
	os.MkdirAll(resolvedPath, 0755)

	// Write to target
	dst, err := os.Create(targetPath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	// Set ownership
	if user != "" && user != "root" {
		agent.RunCommand(ctx, "chown", user+":"+user, targetPath)
	}
	return nil
}

func (s *FileService) Rename(ctx context.Context, user, source, destination string) error {
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
