package agent

import (
	"context"
	"fmt"
	"strings"
)

// ──────────────────────────────────────────────────────
// Runtime version detection
// ──────────────────────────────────────────────────────

// ListPHPVersions returns available and installed PHP versions.
func ListPHPVersions(ctx context.Context) ([]map[string]interface{}, error) {
	known := []string{"7.4", "8.0", "8.1", "8.2", "8.3", "8.4"}
	var versions []map[string]interface{}

	for _, v := range known {
		installed := false
		version := ""
		bin := fmt.Sprintf("php%s", v)
		result, err := RunCommand(ctx, bin, "-v")
		if err == nil && result != nil {
			for _, word := range strings.Fields(result.Output) {
				if len(word) > 0 && word[0] >= '0' && word[0] <= '9' && strings.Contains(word, ".") {
					version = strings.TrimRight(word, ",-()[]")
					installed = true
					break
				}
			}
		}
		versions = append(versions, map[string]interface{}{
			"version":   v,
			"full":      version,
			"installed": installed,
		})
	}
	return versions, nil
}

// ListNodeVersions returns installed and available Node.js versions.
func ListNodeVersions(ctx context.Context) ([]map[string]interface{}, error) {
	known := []string{"16", "18", "20", "22"}
	var versions []map[string]interface{}

	// Check currently active node
	activeVersion := ""
	result, err := RunCommand(ctx, "node", "-v")
	if err == nil && result != nil {
		activeVersion = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(result.Output), "v"))
	}

	for _, v := range known {
		installed := false
		full := ""
		if activeVersion != "" && strings.HasPrefix(activeVersion, v+".") {
			installed = true
			full = activeVersion
		}
		// Also check via nvm or nodesource
		if !installed {
			r, e := RunCommand(ctx, "bash", "-c", fmt.Sprintf("ls /usr/local/n/versions/node/%s.* 2>/dev/null || ls /opt/node/%s.* 2>/dev/null", v, v))
			if e == nil && r != nil && strings.TrimSpace(r.Output) != "" {
				installed = true
				parts := strings.Split(strings.TrimSpace(r.Output), "\n")
				if len(parts) > 0 {
					p := strings.Split(parts[len(parts)-1], "/")
					full = p[len(p)-1]
				}
			}
		}
		versions = append(versions, map[string]interface{}{
			"version":   v,
			"full":      full,
			"installed": installed,
			"active":    full == activeVersion && installed,
		})
	}
	return versions, nil
}

// ListPythonVersions returns installed and available Python versions.
func ListPythonVersions(ctx context.Context) ([]map[string]interface{}, error) {
	known := []string{"3.8", "3.9", "3.10", "3.11", "3.12", "3.13"}
	var versions []map[string]interface{}

	for _, v := range known {
		installed := false
		full := ""
		bin := fmt.Sprintf("python%s", v)
		result, err := RunCommand(ctx, bin, "--version")
		if err == nil && result != nil {
			for _, word := range strings.Fields(result.Output) {
				if len(word) > 0 && word[0] >= '0' && word[0] <= '9' && strings.Contains(word, ".") {
					full = strings.TrimRight(word, ",-()[]")
					installed = true
					break
				}
			}
		}
		versions = append(versions, map[string]interface{}{
			"version":   v,
			"full":      full,
			"installed": installed,
		})
	}
	return versions, nil
}

// ListRubyVersions returns installed and available Ruby versions.
func ListRubyVersions(ctx context.Context) ([]map[string]interface{}, error) {
	known := []string{"3.0", "3.1", "3.2", "3.3"}
	var versions []map[string]interface{}

	activeVersion := ""
	result, err := RunCommand(ctx, "ruby", "-v")
	if err == nil && result != nil {
		for _, word := range strings.Fields(result.Output) {
			if len(word) > 0 && word[0] >= '0' && word[0] <= '9' && strings.Contains(word, ".") {
				activeVersion = strings.TrimRight(word, ",-()[]p")
				break
			}
		}
	}

	for _, v := range known {
		installed := strings.HasPrefix(activeVersion, v+".")
		full := ""
		if installed {
			full = activeVersion
		}
		versions = append(versions, map[string]interface{}{
			"version":   v,
			"full":      full,
			"installed": installed,
		})
	}
	return versions, nil
}

// ListGoVersions returns installed and available Go versions.
func ListGoVersions(ctx context.Context) ([]map[string]interface{}, error) {
	known := []string{"1.20", "1.21", "1.22", "1.23"}
	var versions []map[string]interface{}

	activeVersion := ""
	result, err := RunCommand(ctx, "go", "version")
	if err == nil && result != nil {
		if i := strings.Index(result.Output, "go1."); i >= 0 {
			v := result.Output[i+2:]
			if sp := strings.IndexByte(v, ' '); sp > 0 {
				v = v[:sp]
			}
			activeVersion = strings.TrimSpace(v)
		}
	}

	for _, v := range known {
		installed := strings.HasPrefix(activeVersion, v+".")
		full := ""
		if installed {
			full = activeVersion
		}
		versions = append(versions, map[string]interface{}{
			"version":   v,
			"full":      full,
			"installed": installed,
		})
	}
	return versions, nil
}

// ──────────────────────────────────────────────────────
// Install / Uninstall runtimes
// ──────────────────────────────────────────────────────

// InstallPHP installs a specific PHP version with common extensions.
func InstallPHP(ctx context.Context, version string) error {
	// Add Ondrej PPA if not already added
	RunCommand(ctx, "bash", "-c", "add-apt-repository -y ppa:ondrej/php 2>/dev/null || true")
	RunCommand(ctx, "apt-get", "update", "-y")

	pkgs := fmt.Sprintf("php%s php%s-fpm php%s-cli php%s-common php%s-mysql php%s-xml php%s-curl php%s-mbstring php%s-zip php%s-gd php%s-intl php%s-bcmath php%s-soap",
		version, version, version, version, version, version, version, version, version, version, version, version, version)
	return InstallPackages(ctx, strings.Fields(pkgs)...)
}

// UninstallPHP removes a PHP version.
func UninstallPHP(ctx context.Context, version string) error {
	_, err := RunCommand(ctx, "bash", "-c",
		fmt.Sprintf("apt-get remove -y php%s* && apt-get autoremove -y", version))
	return err
}

// InstallNodeJS installs a specific Node.js LTS version via NodeSource.
func InstallNodeJS(ctx context.Context, majorVersion string) error {
	// Use NodeSource setup script
	_, err := RunCommand(ctx, "bash", "-c",
		fmt.Sprintf("curl -fsSL https://deb.nodesource.com/setup_%s.x | bash - && apt-get install -y nodejs", majorVersion))
	return err
}

// UninstallNodeJS removes Node.js.
func UninstallNodeJS(ctx context.Context) error {
	_, err := RunCommand(ctx, "bash", "-c", "apt-get remove -y nodejs && apt-get autoremove -y")
	return err
}

// InstallPython installs a specific Python version from deadsnakes PPA.
func InstallPython(ctx context.Context, version string) error {
	RunCommand(ctx, "bash", "-c", "add-apt-repository -y ppa:deadsnakes/ppa 2>/dev/null || true")
	RunCommand(ctx, "apt-get", "update", "-y")
	return InstallPackages(ctx, fmt.Sprintf("python%s", version), fmt.Sprintf("python%s-venv", version), fmt.Sprintf("python%s-dev", version))
}

// UninstallPython removes a Python version.
func UninstallPython(ctx context.Context, version string) error {
	_, err := RunCommand(ctx, "bash", "-c",
		fmt.Sprintf("apt-get remove -y python%s* && apt-get autoremove -y", version))
	return err
}

// InstallRuby installs Ruby via apt or rbenv.
func InstallRuby(ctx context.Context, version string) error {
	// Try apt first for available versions
	err := InstallPackages(ctx, "ruby-full")
	if err != nil {
		// Fallback to rbenv
		RunCommand(ctx, "bash", "-c", "apt-get install -y git curl libssl-dev libreadline-dev zlib1g-dev autoconf bison build-essential libyaml-dev libreadline-dev libncurses5-dev libffi-dev libgdbm-dev")
		_, err = RunCommand(ctx, "bash", "-c",
			fmt.Sprintf("curl -fsSL https://github.com/rbenv/ruby-build/raw/HEAD/bin/ruby-build | bash -s -- %s /usr/local/", version))
		return err
	}
	return nil
}

// UninstallRuby removes Ruby.
func UninstallRuby(ctx context.Context) error {
	_, err := RunCommand(ctx, "bash", "-c", "apt-get remove -y ruby* && apt-get autoremove -y")
	return err
}

// InstallGo installs a specific Go version.
func InstallGo(ctx context.Context, version string) error {
	_, err := RunCommand(ctx, "bash", "-c",
		fmt.Sprintf("curl -fsSL https://go.dev/dl/go%s.linux-amd64.tar.gz | tar -C /usr/local -xzf -", version))
	if err != nil {
		return err
	}
	// Ensure Go is in PATH
	RunCommand(ctx, "bash", "-c", `grep -q '/usr/local/go/bin' /etc/profile || echo 'export PATH=$PATH:/usr/local/go/bin' >> /etc/profile`)
	return nil
}

// UninstallGo removes Go.
func UninstallGo(ctx context.Context) error {
	_, err := RunCommand(ctx, "bash", "-c", "rm -rf /usr/local/go")
	return err
}

// ──────────────────────────────────────────────────────
// PHP Extensions
// ──────────────────────────────────────────────────────

// ListPHPExtensions returns installed and available extensions for a PHP version.
func ListPHPExtensions(ctx context.Context, phpVersion string) ([]map[string]interface{}, error) {
	// Get installed extensions
	installedResult, err := RunCommand(ctx, fmt.Sprintf("php%s", phpVersion), "-m")
	installedMap := map[string]bool{}
	if err == nil && installedResult != nil {
		for _, line := range strings.Split(installedResult.Output, "\n") {
			ext := strings.TrimSpace(strings.ToLower(line))
			if ext != "" && !strings.HasPrefix(ext, "[") {
				installedMap[ext] = true
			}
		}
	}

	// Common PHP extensions
	commonExts := []string{
		"bcmath", "bz2", "calendar", "ctype", "curl", "dba",
		"dom", "enchant", "exif", "fileinfo", "ftp", "gd",
		"gettext", "gmp", "iconv", "imagick", "imap", "intl",
		"json", "ldap", "mbstring", "memcached", "mongodb",
		"msgpack", "mysql", "mysqli", "mysqlnd", "opcache",
		"pdo", "pdo_mysql", "pdo_pgsql", "pdo_sqlite", "pgsql",
		"phar", "posix", "readline", "redis", "session",
		"shmop", "simplexml", "soap", "sockets", "sodium",
		"sqlite3", "ssh2", "sysvmsg", "sysvsem", "sysvshm",
		"tidy", "tokenizer", "xml", "xmlreader", "xmlrpc",
		"xmlwriter", "xsl", "zip", "zlib",
	}

	var extensions []map[string]interface{}
	for _, ext := range commonExts {
		extensions = append(extensions, map[string]interface{}{
			"name":      ext,
			"installed": installedMap[ext],
			"package":   fmt.Sprintf("php%s-%s", phpVersion, ext),
		})
	}
	return extensions, nil
}

// InstallPHPExtension installs a PHP extension for a specific version.
func InstallPHPExtension(ctx context.Context, phpVersion, extension string) error {
	pkg := fmt.Sprintf("php%s-%s", phpVersion, extension)
	err := InstallPackages(ctx, pkg)
	if err != nil {
		return err
	}
	// Restart PHP-FPM
	ServiceAction(ctx, fmt.Sprintf("php%s-fpm", phpVersion), "restart")
	return nil
}

// UninstallPHPExtension removes a PHP extension.
func UninstallPHPExtension(ctx context.Context, phpVersion, extension string) error {
	pkg := fmt.Sprintf("php%s-%s", phpVersion, extension)
	_, err := RunCommand(ctx, "apt-get", "remove", "-y", pkg)
	if err != nil {
		return err
	}
	ServiceAction(ctx, fmt.Sprintf("php%s-fpm", phpVersion), "restart")
	return nil
}

// ──────────────────────────────────────────────────────
// PHP-FPM Pool Management
// ──────────────────────────────────────────────────────

// ListPHPFPMPools returns all PHP-FPM pools for a given PHP version.
func ListPHPFPMPools(ctx context.Context, phpVersion string) ([]map[string]interface{}, error) {
	poolDir := fmt.Sprintf("/etc/php/%s/fpm/pool.d/", phpVersion)
	result, err := RunCommand(ctx, "ls", "-1", poolDir)
	if err != nil {
		return []map[string]interface{}{}, nil
	}

	var pools []map[string]interface{}
	for _, file := range strings.Split(result.Output, "\n") {
		file = strings.TrimSpace(file)
		if file == "" || !strings.HasSuffix(file, ".conf") {
			continue
		}
		name := strings.TrimSuffix(file, ".conf")

		// Read pool config for details
		confResult, _ := RunCommand(ctx, "bash", "-c", fmt.Sprintf("cat %s%s", poolDir, file))
		pm := "dynamic"
		maxChildren := "5"
		if confResult != nil {
			for _, line := range strings.Split(confResult.Output, "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "pm =") {
					pm = strings.TrimSpace(strings.TrimPrefix(line, "pm ="))
				}
				if strings.HasPrefix(line, "pm.max_children") {
					parts := strings.SplitN(line, "=", 2)
					if len(parts) == 2 {
						maxChildren = strings.TrimSpace(parts[1])
					}
				}
			}
		}

		// Check if socket exists (pool is active)
		sockPath := fmt.Sprintf("/run/php/php%s-fpm-%s.sock", phpVersion, name)
		active := false
		if _, err := RunCommand(ctx, "test", "-S", sockPath); err == nil {
			active = true
		}

		pools = append(pools, map[string]interface{}{
			"name":         name,
			"file":         file,
			"pm":           pm,
			"max_children": maxChildren,
			"active":       active,
			"php_version":  phpVersion,
		})
	}
	if pools == nil {
		pools = []map[string]interface{}{}
	}
	return pools, nil
}

// GetPHPFPMStatus returns the status of PHP-FPM for a given version.
func GetPHPFPMStatus(ctx context.Context, phpVersion string) (map[string]interface{}, error) {
	service := fmt.Sprintf("php%s-fpm", phpVersion)

	// Check if service is running
	result, err := RunCommand(ctx, "systemctl", "is-active", service)
	running := err == nil && strings.TrimSpace(result.Output) == "active"

	// Get process count
	procResult, _ := RunCommand(ctx, "bash", "-c", fmt.Sprintf("pgrep -c 'php-fpm: pool' || echo 0"))
	processCount := "0"
	if procResult != nil {
		processCount = strings.TrimSpace(procResult.Output)
	}

	return map[string]interface{}{
		"service":       service,
		"running":       running,
		"process_count": processCount,
		"php_version":   phpVersion,
	}, nil
}
