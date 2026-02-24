package agent

import (
	"context"
	"fmt"
	"os"
)

func CreateMailbox(ctx context.Context, email, domain, hashedPassword string) error {
	maildir := fmt.Sprintf("/var/mail/vhosts/%s/%s/", domain, email)
	if err := os.MkdirAll(maildir, 0700); err != nil {
		return err
	}
	RunCommand(ctx, "chown", "-R", "vmail:vmail", fmt.Sprintf("/var/mail/vhosts/%s", domain))
	// TODO: Add to dovecot users file and postfix virtual_mailbox
	_, err := RunCommand(ctx, "postmap", "/etc/postfix/virtual_mailbox")
	if err != nil {
		return err
	}
	_, err = RunCommand(ctx, "systemctl", "reload", "postfix")
	return err
}

func DeleteMailbox(ctx context.Context, email, domain string) error {
	maildir := fmt.Sprintf("/var/mail/vhosts/%s/%s/", domain, email)
	os.RemoveAll(maildir)
	// TODO: Remove from dovecot users file and postfix virtual_mailbox
	RunCommand(ctx, "postmap", "/etc/postfix/virtual_mailbox")
	_, err := RunCommand(ctx, "systemctl", "reload", "postfix")
	return err
}
