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
// Supports multiple versions via the n version manager.
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
		// Check n version manager directory (supports multiple versions)
		r, e := RunCommand(ctx, "bash", "-c",
			fmt.Sprintf("ls -d /usr/local/n/versions/node/%s.* 2>/dev/null | sort -V | tail -1", v))
		if e == nil && r != nil && strings.TrimSpace(r.Output) != "" {
			installed = true
			dir := strings.TrimSpace(r.Output)
			parts := strings.Split(dir, "/")
			full = parts[len(parts)-1]
		}
		// Fallback: check active node (apt-installed, not yet migrated to n)
		if !installed && activeVersion != "" && strings.HasPrefix(activeVersion, v+".") {
			installed = true
			full = activeVersion
		}
		versions = append(versions, map[string]interface{}{
			"version":   v,
			"full":      full,
			"installed": installed,
			"active":    activeVersion != "" && strings.HasPrefix(activeVersion, v+"."),
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
// Supports multiple versions via versioned directories at /opt/ruby/VERSION/.
func ListRubyVersions(ctx context.Context) ([]map[string]interface{}, error) {
	known := []string{"3.0", "3.1", "3.2", "3.3"}

	// Check active version
	activeVersion := ""
	result, _ := RunCommand(ctx, "ruby", "-v")
	if result != nil {
		for _, word := range strings.Fields(result.Output) {
			if len(word) > 0 && word[0] >= '0' && word[0] <= '9' && strings.Contains(word, ".") {
				activeVersion = strings.TrimRight(word, ",-()[]p")
				break
			}
		}
	}

	var versions []map[string]interface{}
	for _, v := range known {
		installed := false
		full := ""
		// Check versioned directory at /opt/ruby/VERSION/
		r, e := RunCommand(ctx, "bash", "-c",
			fmt.Sprintf("/opt/ruby/%s/bin/ruby -v 2>/dev/null", v))
		if e == nil && r != nil {
			for _, word := range strings.Fields(r.Output) {
				if len(word) > 0 && word[0] >= '0' && word[0] <= '9' && strings.Contains(word, ".") {
					full = strings.TrimRight(word, ",-()[]p")
					installed = true
					break
				}
			}
		}
		// Fallback: check system ruby (not yet migrated to /opt/ruby/)
		if !installed && activeVersion != "" && strings.HasPrefix(activeVersion, v+".") {
			installed = true
			full = activeVersion
		}
		versions = append(versions, map[string]interface{}{
			"version":   v,
			"full":      full,
			"installed": installed,
			"active":    activeVersion != "" && strings.HasPrefix(activeVersion, v+"."),
		})
	}
	return versions, nil
}

// ListGoVersions returns installed and available Go versions.
// Supports multiple versions via versioned directories at /opt/go/VERSION/.
func ListGoVersions(ctx context.Context) ([]map[string]interface{}, error) {
	known := []string{"1.20", "1.21", "1.22", "1.23"}

	// Check active version via symlink at /usr/local/go
	activeVersion := ""
	result, err := RunCommand(ctx, "/usr/local/go/bin/go", "version")
	if err != nil {
		result, err = RunCommand(ctx, "go", "version")
	}
	if err == nil && result != nil {
		if i := strings.Index(result.Output, "go1."); i >= 0 {
			v := result.Output[i+2:]
			if sp := strings.IndexByte(v, ' '); sp > 0 {
				v = v[:sp]
			}
			activeVersion = strings.TrimSpace(v)
		}
	}

	var versions []map[string]interface{}
	for _, v := range known {
		installed := false
		full := ""
		// Check versioned directory at /opt/go/VERSION/
		r, e := RunCommand(ctx, "bash", "-c",
			fmt.Sprintf("/opt/go/%s/bin/go version 2>/dev/null", v))
		if e == nil && r != nil {
			if i := strings.Index(r.Output, "go1."); i >= 0 {
				ver := r.Output[i+2:]
				if sp := strings.IndexByte(ver, ' '); sp > 0 {
					ver = ver[:sp]
				}
				full = strings.TrimSpace(ver)
				installed = true
			}
		}
		// Fallback: check /usr/local/go (not yet migrated to /opt/go/)
		if !installed && activeVersion != "" && strings.HasPrefix(activeVersion, v+".") {
			installed = true
			full = activeVersion
		}
		versions = append(versions, map[string]interface{}{
			"version":   v,
			"full":      full,
			"installed": installed,
			"active":    activeVersion != "" && strings.HasPrefix(activeVersion, v+"."),
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

// InstallNodeJS installs a specific Node.js version via the n version manager.
// Multiple versions can coexist in /usr/local/n/versions/node/.
func InstallNodeJS(ctx context.Context, majorVersion string) error {
	// Ensure n version manager is installed
	if _, err := RunCommand(ctx, "bash", "-c", "test -f /usr/local/bin/n"); err != nil {
		// Migration: detect existing NodeSource node version before removing
		existingMajor := ""
		result, _ := RunCommand(ctx, "node", "-v")
		if result != nil {
			v := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(result.Output), "v"))
			if parts := strings.SplitN(v, ".", 2); len(parts) > 0 && parts[0] != "" {
				existingMajor = parts[0]
			}
		}
		// Remove NodeSource apt package
		RunLongCommand(ctx, "bash", "-c", "apt-get remove -y nodejs 2>/dev/null || true")
		RunCommand(ctx, "bash", "-c", "rm -f /etc/apt/sources.list.d/nodesource* /etc/apt/keyrings/nodesource.gpg 2>/dev/null || true")
		// Install n version manager
		_, err = RunLongCommand(ctx, "bash", "-c",
			"curl -fsSL https://raw.githubusercontent.com/tj/n/master/bin/n -o /usr/local/bin/n && chmod +x /usr/local/bin/n && mkdir -p /usr/local/n/versions/node")
		if err != nil {
			return fmt.Errorf("failed to install n version manager: %w", err)
		}
		// Reinstall the old version via n (preserves it alongside the new one)
		if existingMajor != "" && existingMajor != majorVersion {
			RunLongCommand(ctx, "bash", "-c", fmt.Sprintf("n %s 2>/dev/null || true", existingMajor))
		}
	}
	// Install the requested version via n (stores in /usr/local/n/versions/node/ and sets as active)
	_, err := RunLongCommand(ctx, "bash", "-c", fmt.Sprintf("n %s", majorVersion))
	return err
}

// UninstallNodeJS removes a specific Node.js major version from the n version manager.
func UninstallNodeJS(ctx context.Context, majorVersion string) error {
	// Remove the version directory from n
	RunCommand(ctx, "bash", "-c",
		fmt.Sprintf("rm -rf /usr/local/n/versions/node/%s.*", majorVersion))
	// If the active version was removed, switch to another or clean up
	result, _ := RunCommand(ctx, "node", "-v")
	needSwitch := false
	if result != nil {
		active := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(result.Output), "v"))
		if strings.HasPrefix(active, majorVersion+".") {
			needSwitch = true
		}
	}
	if needSwitch {
		r, _ := RunCommand(ctx, "bash", "-c", "ls /usr/local/n/versions/node/ 2>/dev/null | sort -V | tail -1")
		if r != nil && strings.TrimSpace(r.Output) != "" {
			RunCommand(ctx, "bash", "-c", fmt.Sprintf("n %s", strings.TrimSpace(r.Output)))
		} else {
			RunCommand(ctx, "bash", "-c", "rm -f /usr/local/bin/node /usr/local/bin/npm /usr/local/bin/npx /usr/local/bin/corepack")
		}
	}
	return nil
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

// InstallRuby installs a specific Ruby version to /opt/ruby/VERSION/.
// Multiple versions can coexist; the most recently installed becomes active via symlinks.
func InstallRuby(ctx context.Context, version string) error {
	// Install build dependencies
	RunLongCommand(ctx, "bash", "-c", "apt-get install -y git curl libssl-dev libreadline-dev zlib1g-dev autoconf bison build-essential libyaml-dev libncurses5-dev libffi-dev libgdbm-dev libgdbm6 2>/dev/null || true")
	// Install ruby-build if not present
	if _, err := RunCommand(ctx, "bash", "-c", "command -v ruby-build >/dev/null 2>&1"); err != nil {
		RunLongCommand(ctx, "bash", "-c", "rm -rf /tmp/ruby-build && git clone --depth 1 https://github.com/rbenv/ruby-build.git /tmp/ruby-build && PREFIX=/usr/local /tmp/ruby-build/install.sh")
	}
	// Migration: clean up old /usr/local ruby (not managed by /opt/ruby/)
	r, _ := RunCommand(ctx, "bash", "-c", "test -f /usr/local/bin/ruby && ! readlink -f /usr/local/bin/ruby 2>/dev/null | grep -q /opt/ruby && echo migrate")
	if r != nil && strings.TrimSpace(r.Output) == "migrate" {
		RunCommand(ctx, "bash", "-c", "rm -rf /usr/local/lib/ruby /usr/local/share/ruby /usr/local/include/ruby* && rm -f /usr/local/bin/ruby /usr/local/bin/gem /usr/local/bin/irb /usr/local/bin/bundle /usr/local/bin/bundler /usr/local/bin/erb /usr/local/bin/rake /usr/local/bin/rdoc /usr/local/bin/ri")
		RunLongCommand(ctx, "bash", "-c", "apt-get remove -y ruby ruby-full ruby-dev 2>/dev/null; apt-get autoremove -y 2>/dev/null; true")
	}
	// Find latest patch version for requested major.minor
	fullVersion := version + ".0"
	result, err := RunCommand(ctx, "bash", "-c",
		fmt.Sprintf(`ruby-build --definitions 2>/dev/null | grep "^%s\." | sort -V | tail -1`, version))
	if err == nil && result != nil && strings.TrimSpace(result.Output) != "" {
		fullVersion = strings.TrimSpace(result.Output)
	}
	// Install to versioned prefix
	prefix := fmt.Sprintf("/opt/ruby/%s", version)
	RunCommand(ctx, "rm", "-rf", prefix)
	RunCommand(ctx, "mkdir", "-p", prefix)
	_, err = RunLongCommand(ctx, "bash", "-c",
		fmt.Sprintf(`RUBY_CONFIGURE_OPTS="--disable-install-doc" MAKE_OPTS="-j$(nproc)" ruby-build %s %s`, fullVersion, prefix))
	if err != nil {
		return err
	}
	// Symlink binaries to /usr/local/bin (makes this the active version)
	bins := []string{"ruby", "gem", "irb", "bundle", "bundler", "erb", "rake", "rdoc", "ri"}
	for _, bin := range bins {
		RunCommand(ctx, "bash", "-c", fmt.Sprintf("test -f %s/bin/%s && ln -sfn %s/bin/%s /usr/local/bin/%s || true", prefix, bin, prefix, bin, bin))
	}
	return nil
}

// UninstallRuby removes a specific Ruby version from /opt/ruby/VERSION/.
func UninstallRuby(ctx context.Context, version string) error {
	prefix := fmt.Sprintf("/opt/ruby/%s", version)
	RunCommand(ctx, "rm", "-rf", prefix)
	// If active ruby points to this version, switch to another or clean up
	r, _ := RunCommand(ctx, "bash", "-c", "readlink -f /usr/local/bin/ruby 2>/dev/null")
	if r != nil && strings.Contains(r.Output, prefix) {
		other, _ := RunCommand(ctx, "bash", "-c", "ls -d /opt/ruby/*/bin/ruby 2>/dev/null | sort -V | tail -1")
		if other != nil && strings.TrimSpace(other.Output) != "" {
			otherPrefix := strings.TrimSuffix(strings.TrimSpace(other.Output), "/bin/ruby")
			bins := []string{"ruby", "gem", "irb", "bundle", "bundler", "erb", "rake", "rdoc", "ri"}
			for _, bin := range bins {
				RunCommand(ctx, "bash", "-c", fmt.Sprintf("test -f %s/bin/%s && ln -sfn %s/bin/%s /usr/local/bin/%s || true", otherPrefix, bin, otherPrefix, bin, bin))
			}
		} else {
			RunCommand(ctx, "bash", "-c", "rm -f /usr/local/bin/ruby /usr/local/bin/gem /usr/local/bin/irb /usr/local/bin/bundle /usr/local/bin/bundler /usr/local/bin/erb /usr/local/bin/rake /usr/local/bin/rdoc /usr/local/bin/ri")
		}
	}
	return nil
}

// InstallGo installs a specific Go version to /opt/go/VERSION/.
// Multiple versions can coexist; /usr/local/go symlinks to the active version.
func InstallGo(ctx context.Context, version string) error {
	shortVersion := version
	// If short version like "1.22", resolve the latest patch version from go.dev
	if len(strings.Split(version, ".")) == 2 {
		result, err := RunCommand(ctx, "bash", "-c",
			fmt.Sprintf(`curl -fsSL "https://go.dev/dl/?mode=json&include=all" | grep -oP '"version":\s*"go%s\.\d+"' | head -1 | grep -oP '%s\.\d+'`, version, version))
		if err == nil && result != nil && strings.TrimSpace(result.Output) != "" {
			version = strings.TrimSpace(result.Output)
		} else {
			version = version + ".0"
		}
	}
	// Extract short version (major.minor) for directory name
	if parts := strings.Split(version, "."); len(parts) >= 2 {
		shortVersion = parts[0] + "." + parts[1]
	}
	// Migration: if /usr/local/go is a real directory (not symlink to /opt/go/), move it
	linkTarget, _ := RunCommand(ctx, "bash", "-c", "readlink /usr/local/go 2>/dev/null")
	if linkTarget == nil || !strings.Contains(strings.TrimSpace(linkTarget.Output), "/opt/go") {
		existingVersion := ""
		r, _ := RunCommand(ctx, "/usr/local/go/bin/go", "version")
		if r != nil {
			if i := strings.Index(r.Output, "go1."); i >= 0 {
				v := r.Output[i+2:]
				if sp := strings.IndexByte(v, ' '); sp > 0 {
					v = v[:sp]
				}
				existingVersion = strings.TrimSpace(v)
			}
		}
		if existingVersion != "" {
			if ep := strings.Split(existingVersion, "."); len(ep) >= 2 {
				migrateDir := fmt.Sprintf("/opt/go/%s.%s", ep[0], ep[1])
				RunCommand(ctx, "mkdir", "-p", "/opt/go")
				RunCommand(ctx, "bash", "-c", fmt.Sprintf("mv /usr/local/go %s", migrateDir))
			}
		} else {
			RunCommand(ctx, "rm", "-rf", "/usr/local/go")
		}
	}
	// Install to versioned directory
	dir := fmt.Sprintf("/opt/go/%s", shortVersion)
	RunCommand(ctx, "rm", "-rf", dir)
	RunCommand(ctx, "mkdir", "-p", dir)
	_, err := RunLongCommand(ctx, "bash", "-c",
		fmt.Sprintf("curl -fsSL https://go.dev/dl/go%s.linux-amd64.tar.gz -o /tmp/go.tar.gz && tar -C %s --strip-components=1 -xzf /tmp/go.tar.gz && rm -f /tmp/go.tar.gz", version, dir))
	if err != nil {
		return err
	}
	// Update active symlink
	RunCommand(ctx, "rm", "-f", "/usr/local/go")
	RunCommand(ctx, "ln", "-sfn", dir, "/usr/local/go")
	RunCommand(ctx, "bash", "-c", `grep -q '/usr/local/go/bin' /etc/profile || echo 'export PATH=$PATH:/usr/local/go/bin' >> /etc/profile`)
	return nil
}

// UninstallGo removes a specific Go version from /opt/go/VERSION/.
func UninstallGo(ctx context.Context, version string) error {
	dir := fmt.Sprintf("/opt/go/%s", version)
	RunCommand(ctx, "rm", "-rf", dir)
	// If active symlink points to removed version, switch to another or clean up
	linkTarget, _ := RunCommand(ctx, "bash", "-c", "readlink /usr/local/go 2>/dev/null")
	if linkTarget != nil && strings.Contains(strings.TrimSpace(linkTarget.Output), dir) {
		other, _ := RunCommand(ctx, "bash", "-c", "ls -d /opt/go/*/bin/go 2>/dev/null | sort -V | tail -1")
		if other != nil && strings.TrimSpace(other.Output) != "" {
			otherDir := strings.TrimSuffix(strings.TrimSpace(other.Output), "/bin/go")
			RunCommand(ctx, "rm", "-f", "/usr/local/go")
			RunCommand(ctx, "ln", "-sfn", otherDir, "/usr/local/go")
		} else {
			RunCommand(ctx, "rm", "-f", "/usr/local/go")
		}
	}
	return nil
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
