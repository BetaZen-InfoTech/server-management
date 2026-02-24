package agent

import (
	"context"
	"fmt"
)

func InstallWordPress(ctx context.Context, user, path, dbName, dbUser, dbPass, dbHost, siteURL, title, adminUser, adminPass, adminEmail string) error {
	wpPath := fmt.Sprintf("/home/%s/public_html%s", user, path)
	RunCommandAsUser(ctx, user, fmt.Sprintf("wp core download --path=%s", wpPath))
	RunCommandAsUser(ctx, user, fmt.Sprintf("wp config create --path=%s --dbname=%s --dbuser=%s --dbpass=%s --dbhost=%s", wpPath, dbName, dbUser, dbPass, dbHost))
	_, err := RunCommandAsUser(ctx, user, fmt.Sprintf("wp core install --path=%s --url=%s --title='%s' --admin_user=%s --admin_password=%s --admin_email=%s", wpPath, siteURL, title, adminUser, adminPass, adminEmail))
	return err
}

func WPCLICommand(ctx context.Context, user, wpPath, command string) (string, error) {
	result, err := RunCommandAsUser(ctx, user, fmt.Sprintf("wp %s --path=%s", command, wpPath))
	if err != nil {
		return "", err
	}
	return result.Output, nil
}

func WPSecurityScan(ctx context.Context, user, wpPath string) (map[string]interface{}, error) {
	result := make(map[string]interface{})
	if out, err := WPCLICommand(ctx, user, wpPath, "core verify-checksums"); err == nil {
		result["core_integrity"] = out
	}
	if out, err := WPCLICommand(ctx, user, wpPath, "plugin list --update=available --format=json"); err == nil {
		result["outdated_plugins"] = out
	}
	return result, nil
}
