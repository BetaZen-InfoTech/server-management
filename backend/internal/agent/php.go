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

func CreatePHPPool(ctx context.Context, user, phpVersion string) error {
	config := fmt.Sprintf(phpPoolTemplate, user, user, user, phpVersion, user, user)
	path := fmt.Sprintf("/etc/php/%s/fpm/pool.d/%s.conf", phpVersion, user)
	if err := os.WriteFile(path, []byte(config), 0644); err != nil {
		return err
	}
	_, err := RunCommand(ctx, "systemctl", "reload", fmt.Sprintf("php%s-fpm", phpVersion))
	return err
}

func DeletePHPPool(ctx context.Context, user string) error {
	versions := []string{"7.4", "8.0", "8.1", "8.2", "8.3"}
	for _, v := range versions {
		path := fmt.Sprintf("/etc/php/%s/fpm/pool.d/%s.conf", v, user)
		os.Remove(path)
		RunCommand(ctx, "systemctl", "reload", fmt.Sprintf("php%s-fpm", v))
	}
	return nil
}

func SwitchPHPVersion(ctx context.Context, user, oldVersion, newVersion string) error {
	os.Remove(fmt.Sprintf("/etc/php/%s/fpm/pool.d/%s.conf", oldVersion, user))
	RunCommand(ctx, "systemctl", "reload", fmt.Sprintf("php%s-fpm", oldVersion))
	return CreatePHPPool(ctx, user, newVersion)
}
