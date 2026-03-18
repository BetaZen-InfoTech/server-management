package agent

import (
	"context"
	"fmt"
	"strings"
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

// ListAllZones returns all zone names from PowerDNS.
func ListAllZones(ctx context.Context) ([]string, error) {
	result, err := RunCommand(ctx, "pdnsutil", "list-all-zones")
	if err != nil {
		return nil, err
	}
	var zones []string
	for _, line := range strings.Split(strings.TrimSpace(result.Output), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			// Remove trailing dot if present
			zones = append(zones, strings.TrimSuffix(line, "."))
		}
	}
	return zones, nil
}

// ParsedRecord represents a DNS record parsed from pdnsutil output.
type ParsedRecord struct {
	Name  string
	TTL   string
	Type  string
	Value string
}

// ListZoneRecords parses records from pdnsutil list-zone output.
func ListZoneRecords(ctx context.Context, domain string) ([]ParsedRecord, error) {
	result, err := RunCommand(ctx, "pdnsutil", "list-zone", domain)
	if err != nil {
		return nil, err
	}
	var records []ParsedRecord
	for _, line := range strings.Split(strings.TrimSpace(result.Output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Format: name TTL IN TYPE value
		parts := strings.Fields(line)
		if len(parts) < 4 {
			continue
		}
		name := strings.TrimSuffix(parts[0], ".")
		ttl := parts[1]
		// parts[2] is "IN"
		rtype := parts[3]
		value := ""
		if len(parts) > 4 {
			value = strings.Join(parts[4:], " ")
		}

		// Convert FQDN name to relative name
		suffix := "." + domain
		if name == domain {
			name = "@"
		} else if strings.HasSuffix(name, suffix) {
			name = strings.TrimSuffix(name, suffix)
		}

		records = append(records, ParsedRecord{
			Name:  name,
			TTL:   ttl,
			Type:  rtype,
			Value: value,
		})
	}
	return records, nil
}
