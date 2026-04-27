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
		// pdnsutil delete-rrset on an already-gone rrset returns
		// non-zero ("Could not find rrset"). The desired post-state
		// ("rrset is gone") is already true, so treat that as success.
		// Anything else is a real failure (DB connection, perms, etc.)
		// and bubbles up.
		if strings.Contains(strings.ToLower(err.Error()), "could not find") ||
			strings.Contains(strings.ToLower(err.Error()), "no such") {
			_, _ = RunCommand(ctx, "pdns_control", "reload")
			return nil
		}
		return err
	}
	_, err = RunCommand(ctx, "pdns_control", "reload")
	return err
}

// ReplaceDNSRecordSet atomically sets the entire rrset for (zone, name,
// type) to exactly `values`. Pass an empty values slice to delete the
// rrset. PowerDNS stores TTL at the rrset level (NOT per value), so all
// values share the same ttl — the caller is responsible for picking
// one (typically the min of any sibling's TTL).
//
// This is the only safe way to manipulate an rrset that has multiple
// values: pdnsutil add-record appends without checking for duplicates
// and pdnsutil delete-rrset wipes everything, neither of which can
// express "remove just this one value, keep the rest". replace-rrset
// takes the desired post-state and pdnsutil computes the diff itself.
//
// Reloads pdns once at the end so subsequent reads see the new state.
// Idempotent: calling with the same values is a no-op from a serving
// standpoint (zone serial still bumps once on the panel side).
func ReplaceDNSRecordSet(ctx context.Context, domain, name, recordType, ttl string, values []string) error {
	if len(values) == 0 {
		return DeleteDNSRecord(ctx, domain, name, recordType)
	}
	args := []string{"replace-rrset", domain, name, recordType, ttl}
	args = append(args, values...)
	if _, err := RunCommand(ctx, "pdnsutil", args...); err != nil {
		return err
	}
	_, err := RunCommand(ctx, "pdns_control", "reload")
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
