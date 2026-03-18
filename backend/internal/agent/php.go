package agent

import (
	"context"
	"fmt"
	"os"
)

const phpPoolTemplate = `[%s]
user = %s
group = %s
listen = /run/php/php%s-fpm-%s.sock
listen.owner = www-data
listen.group = www-data
pm = dynamic
pm.max_children = 5
pm.start_servers = 2
pm.min_spare_servers = 1
pm.max_spare_servers = 3
pm.max_requests = 500
php_admin_value[open_basedir] = /home/%s:/tmp:/usr/share/php
php_admin_value[disable_functions] = exec,passthru,shell_exec,system,proc_open,popen
php_admin_value[upload_max_filesize] = 100M
php_admin_value[post_max_size] = 100M
php_admin_value[memory_limit] = 256M
php_admin_value[max_execution_time] = 300
`

// CreatePHPPool creates a PHP-FPM pool. poolName is used for the pool name and
// socket file (typically the domain name), while user is the Linux user the pool runs as.
func CreatePHPPool(ctx context.Context, poolName, user, phpVersion string) error {
	config := fmt.Sprintf(phpPoolTemplate, poolName, user, user, phpVersion, poolName, user)
	path := fmt.Sprintf("/etc/php/%s/fpm/pool.d/%s.conf", phpVersion, poolName)
	if err := os.WriteFile(path, []byte(config), 0644); err != nil {
		return err
	}
	_, err := RunCommand(ctx, "systemctl", "reload", fmt.Sprintf("php%s-fpm", phpVersion))
	return err
}

// DeletePHPPool removes a PHP-FPM pool by poolName (domain name).
func DeletePHPPool(ctx context.Context, poolName string) error {
	versions := []string{"7.4", "8.0", "8.1", "8.2", "8.3"}
	for _, v := range versions {
		path := fmt.Sprintf("/etc/php/%s/fpm/pool.d/%s.conf", v, poolName)
		os.Remove(path)
		RunCommand(ctx, "systemctl", "reload", fmt.Sprintf("php%s-fpm", v))
	}
	return nil
}

// SwitchPHPVersion switches the PHP version for a pool (identified by poolName/domain).
func SwitchPHPVersion(ctx context.Context, poolName, user, oldVersion, newVersion string) error {
	os.Remove(fmt.Sprintf("/etc/php/%s/fpm/pool.d/%s.conf", oldVersion, poolName))
	RunCommand(ctx, "systemctl", "reload", fmt.Sprintf("php%s-fpm", oldVersion))
	return CreatePHPPool(ctx, poolName, user, newVersion)
}
