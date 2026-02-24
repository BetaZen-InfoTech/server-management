package agent

import "context"

func AllowPort(ctx context.Context, port, protocol, source string) error {
	args := []string{"allow"}
	if source != "" {
		args = append(args, "from", source, "to", "any", "port", port, "proto", protocol)
	} else {
		args = append(args, port+"/"+protocol)
	}
	_, err := RunCommand(ctx, "ufw", args...)
	return err
}

func DenyPort(ctx context.Context, port, protocol string) error {
	_, err := RunCommand(ctx, "ufw", "deny", port+"/"+protocol)
	return err
}

func BlockIP(ctx context.Context, ip string) error {
	_, err := RunCommand(ctx, "ufw", "insert", "1", "deny", "from", ip)
	return err
}

func UnblockIP(ctx context.Context, ip string) error {
	_, err := RunCommand(ctx, "ufw", "delete", "deny", "from", ip)
	return err
}

func GetUFWStatus(ctx context.Context) (string, error) {
	result, err := RunCommand(ctx, "ufw", "status", "verbose")
	if err != nil {
		return "", err
	}
	return result.Output, nil
}

func Fail2BanUnban(ctx context.Context, jail, ip string) error {
	_, err := RunCommand(ctx, "fail2ban-client", "set", jail, "unbanip", ip)
	return err
}
