package agent

import (
	"context"
	"fmt"
)

func CreateDNSZone(ctx context.Context, domain, serverIP, adminEmail string, nameservers []string) error {
	_, err := RunCommand(ctx, "pdnsutil", "create-zone", domain)
	if err != nil {
		return err
	}

	// Fix SOA record
	primaryNS := "dns1.betazeninfotech.com"
	if len(nameservers) > 0 {
		primaryNS = nameservers[0]
	}
	hostmaster := "hostmaster." + domain
	if adminEmail != "" {
		hostmaster = adminEmail
	}
	soa := fmt.Sprintf("%s %s 1 10800 3600 604800 3600", primaryNS, hostmaster)
	RunCommand(ctx, "pdnsutil", "replace-rrset", domain, "", "SOA", "3600", soa)

	// Add NS records
	for _, ns := range nameservers {
		RunCommand(ctx, "pdnsutil", "add-record", domain, "@", "NS", "3600", ns)
	}

	// Add default records
	RunCommand(ctx, "pdnsutil", "add-record", domain, "@", "A", "3600", serverIP)
	RunCommand(ctx, "pdnsutil", "add-record", domain, "www", "CNAME", "3600", domain+".")
	_, err = RunCommand(ctx, "pdns_control", "reload")
	return err
}

func DeleteDNSZone(ctx context.Context, domain string) error {
	_, err := RunCommand(ctx, "pdnsutil", "delete-zone", domain)
	if err != nil {
		return err
	}
	_, err = RunCommand(ctx, "pdns_control", "reload")
	return err
}

func AddDNSRecord(ctx context.Context, domain, name, recordType, ttl, value string) error {
	_, err := RunCommand(ctx, "pdnsutil", "add-record", domain, name, recordType, ttl, value)
	if err != nil {
		return err
	}
	_, err = RunCommand(ctx, "pdns_control", "reload")
	return err
}

func DeleteDNSRecord(ctx context.Context, domain, name, recordType string) error {
	_, err := RunCommand(ctx, "pdnsutil", "delete-rrset", domain, name, recordType)
	if err != nil {
		return err
	}
	_, err = RunCommand(ctx, "pdns_control", "reload")
	return err
}

func ExportDNSZone(ctx context.Context, domain string) (string, error) {
	result, err := RunCommand(ctx, "pdnsutil", "list-zone", domain)
	if err != nil {
		return "", err
	}
	return result.Output, nil
}

func EnableDNSSEC(ctx context.Context, domain string) error {
	_, err := RunCommand(ctx, "pdnsutil", "secure-zone", domain)
	if err != nil {
		return err
	}
	fmt.Println("DNSSEC enabled for", domain)
	return nil
}
