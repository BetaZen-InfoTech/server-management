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
	RunLongCommand(ctx, "bash", "-c", "add-apt-repository -y ppa:ondrej/php 2>/dev/null || true")
	RunLongCommand(ctx, "apt-get", "update", "-y")

	pkgs := fmt.Sprintf("php%s php%s-fpm php%s-cli php%s-common php%s-mysql php%s-xml php%s-curl php%s-mbstring php%s-zip php%s-gd php%s-intl php%s-bcmath php%s-soap",
		version, version, version, version, version, version, version, version, version, version, version, version, version)
	return InstallPackages(ctx, strings.Fields(pkgs)...)
}

// UninstallPHP removes a PHP version.
func UninstallPHP(ctx context.Context, version string) error {
	_, err := RunLongCommand(ctx, "bash", "-c",
		fmt.Sprintf("apt-get remove -y php%s* && apt-get autoremove -y", version))
	return err
}

// InstallNodeJS installs a specific Node.js LTS version via NodeSource.
// Removes existing Node.js first since only one major version can be installed at a time.
func InstallNodeJS(ctx context.Context, majorVersion string) error {
	// Remove existing nodejs to allow switching between major versions
	RunLongCommand(ctx, "bash", "-c", "apt-get remove -y nodejs 2>/dev/null || true")
	// Remove old NodeSource repo config to avoid version conflicts
	RunCommand(ctx, "bash", "-c", "rm -f /etc/apt/sources.list.d/nodesource* /etc/apt/keyrings/nodesource.gpg 2>/dev/null || true")
	// Use NodeSource setup script for the requested major version
	_, err := RunLongCommand(ctx, "bash", "-c",
		fmt.Sprintf("curl -fsSL https://deb.nodesource.com/setup_%s.x | bash - && apt-get install -y nodejs", majorVersion))
	return err
}

// UninstallNodeJS removes Node.js.
func UninstallNodeJS(ctx context.Context) error {
	_, err := RunLongCommand(ctx, "bash", "-c", "apt-get remove -y nodejs && apt-get autoremove -y")
	return err
}

// InstallPython installs a specific Python version from deadsnakes PPA.
func InstallPython(ctx context.Context, version string) error {
	RunLongCommand(ctx, "bash", "-c", "add-apt-repository -y ppa:deadsnakes/ppa 2>/dev/null || true")
	RunLongCommand(ctx, "apt-get", "update", "-y")
	return InstallPackages(ctx, fmt.Sprintf("python%s", version), fmt.Sprintf("python%s-venv", version), fmt.Sprintf("python%s-dev", version))
}

// UninstallPython removes a Python version.
func UninstallPython(ctx context.Context, version string) error {
	_, err := RunLongCommand(ctx, "bash", "-c",
		fmt.Sprintf("apt-get remove -y python%s* && apt-get autoremove -y", version))
	return err
}

// InstallRuby installs a specific Ruby version.
// Removes existing Ruby first since only one version can be active at a time.
func InstallRuby(ctx context.Context, version string) error {
	// Remove existing ruby to allow version switching
	RunLongCommand(ctx, "bash", "-c", "apt-get remove -y ruby ruby-full ruby-dev 2>/dev/null; apt-get autoremove -y 2>/dev/null; rm -f /usr/local/bin/ruby /usr/local/bin/gem /usr/local/bin/irb /usr/local/bin/bundle /usr/local/bin/bundler /usr/local/bin/erb /usr/local/bin/rake /usr/local/bin/rdoc /usr/local/bin/ri 2>/dev/null; true")
	// Install build dependencies
	RunLongCommand(ctx, "bash", "-c", "apt-get install -y git curl libssl-dev libreadline-dev zlib1g-dev autoconf bison build-essential libyaml-dev libncurses5-dev libffi-dev libgdbm-dev libgdbm6 2>/dev/null || true")
	// Install ruby-build
	RunLongCommand(ctx, "bash", "-c", "rm -rf /tmp/ruby-build && git clone --depth 1 https://github.com/rbenv/ruby-build.git /tmp/ruby-build && PREFIX=/usr/local /tmp/ruby-build/install.sh")
	// Find latest patch version for requested major.minor
	fullVersion := version + ".0"
	result, err := RunCommand(ctx, "bash", "-c",
		fmt.Sprintf(`ruby-build --definitions 2>/dev/null | grep "^%s\." | sort -V | tail -1`, version))
	if err == nil && result != nil && strings.TrimSpace(result.Output) != "" {
		fullVersion = strings.TrimSpace(result.Output)
	}
	// Build and install Ruby (disable docs to speed up, use all CPU cores)
	_, err = RunLongCommand(ctx, "bash", "-c",
		fmt.Sprintf(`RUBY_CONFIGURE_OPTS="--disable-install-doc" MAKE_OPTS="-j$(nproc)" ruby-build %s /usr/local`, fullVersion))
	return err
}

// UninstallRuby removes Ruby (handles both apt and ruby-build installs).
func UninstallRuby(ctx context.Context) error {
	// Remove apt-installed ruby
	RunLongCommand(ctx, "bash", "-c", "apt-get remove -y ruby ruby-full ruby-dev 2>/dev/null; apt-get autoremove -y 2>/dev/null; true")
	// Clean up ruby-build installs at /usr/local
	_, err := RunCommand(ctx, "bash", "-c", "rm -rf /usr/local/lib/ruby /usr/local/share/ruby /usr/local/include/ruby* && rm -f /usr/local/bin/ruby /usr/local/bin/gem /usr/local/bin/irb /usr/local/bin/bundle /usr/local/bin/bundler /usr/local/bin/erb /usr/local/bin/rdoc /usr/local/bin/ri /usr/local/bin/rake")
	return err
}

// InstallGo installs a specific Go version.
// version can be short (e.g. "1.22") or full (e.g. "1.22.5").
func InstallGo(ctx context.Context, version string) error {
	// If short version like "1.22", resolve the latest patch version from go.dev
	if len(strings.Split(version, ".")) == 2 {
		result, err := RunCommand(ctx, "bash", "-c",
			fmt.Sprintf(`curl -fsSL "https://go.dev/dl/?mode=json&include=all" | grep -oP '"version":\s*"go%s\.\d+"' | head -1 | grep -oP '%s\.\d+'`, version, version))
		if err == nil && result != nil && strings.TrimSpace(result.Output) != "" {
			version = strings.TrimSpace(result.Output)
		} else {
			// Fallback: try .0
			version = version + ".0"
		}
	}
	// Remove existing Go installation before installing new one
	RunCommand(ctx, "bash", "-c", "rm -rf /usr/local/go")
	_, err := RunLongCommand(ctx, "bash", "-c",
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
// Includes both enabled (.conf) and disabled (.conf.disabled) pools.
func ListPHPFPMPools(ctx context.Context, phpVersion string) ([]map[string]interface{}, error) {
	poolDir := fmt.Sprintf("/etc/php/%s/fpm/pool.d/", phpVersion)
	result, err := RunCommand(ctx, "ls", "-1", poolDir)
	if err != nil {
		return []map[string]interface{}{}, nil
	}

	var pools []map[string]interface{}
	for _, file := range strings.Split(result.Output, "\n") {
		file = strings.TrimSpace(file)
		if file == "" {
			continue
		}

		// Accept both .conf (enabled) and .conf.disabled (disabled)
		enabled := true
		name := ""
		if strings.HasSuffix(file, ".conf.disabled") {
			name = strings.TrimSuffix(file, ".conf.disabled")
			enabled = false
		} else if strings.HasSuffix(file, ".conf") {
			name = strings.TrimSuffix(file, ".conf")
		} else {
			continue
		}

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

		// Check if socket exists (pool is active/running)
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
			"enabled":      enabled,
			"php_version":  phpVersion,
		})
	}
	if pools == nil {
		pools = []map[string]interface{}{}
	}
	return pools, nil
}

// EnablePHPFPMPool enables a disabled FPM pool by renaming .conf.disabled back to .conf.
func EnablePHPFPMPool(ctx context.Context, phpVersion, poolName string) error {
	poolDir := fmt.Sprintf("/etc/php/%s/fpm/pool.d/", phpVersion)
	disabledPath := poolDir + poolName + ".conf.disabled"
	enabledPath := poolDir + poolName + ".conf"

	if _, err := RunCommand(ctx, "mv", disabledPath, enabledPath); err != nil {
		return fmt.Errorf("failed to enable pool %s: %w", poolName, err)
	}
	_, err := RunCommand(ctx, "systemctl", "reload", fmt.Sprintf("php%s-fpm", phpVersion))
	return err
}

// DisablePHPFPMPool disables an FPM pool by renaming .conf to .conf.disabled.
func DisablePHPFPMPool(ctx context.Context, phpVersion, poolName string) error {
	poolDir := fmt.Sprintf("/etc/php/%s/fpm/pool.d/", phpVersion)
	enabledPath := poolDir + poolName + ".conf"
	disabledPath := poolDir + poolName + ".conf.disabled"

	if _, err := RunCommand(ctx, "mv", enabledPath, disabledPath); err != nil {
		return fmt.Errorf("failed to disable pool %s: %w", poolName, err)
	}
	_, err := RunCommand(ctx, "systemctl", "reload", fmt.Sprintf("php%s-fpm", phpVersion))
	return err
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
