package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

type FirewallService struct {
	db *mongo.Database
}

func NewFirewallService(db *mongo.Database) *FirewallService {
	return &FirewallService{db: db}
}

// GetStatus returns the current firewall status including rule and block counts.
func (s *FirewallService) GetStatus(ctx context.Context) (*models.FirewallStatus, error) {
	status := &models.FirewallStatus{}

	// Parse UFW status
	if output, err := agent.GetUFWStatus(ctx); err == nil {
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "Status:") {
				status.Enabled = strings.Contains(line, "active")
			}
			if strings.HasPrefix(line, "Default:") {
				parts := strings.Split(line, ",")
				for _, p := range parts {
					p = strings.TrimSpace(p)
					if strings.Contains(p, "incoming") {
						if strings.Contains(p, "deny") {
							status.DefaultIncoming = "deny"
						} else {
							status.DefaultIncoming = "allow"
						}
					}
					if strings.Contains(p, "outgoing") {
						if strings.Contains(p, "allow") {
							status.DefaultOutgoing = "allow"
						} else {
							status.DefaultOutgoing = "deny"
						}
					}
				}
			}
		}
	}

	// Count rules and blocked IPs from DB
	rulesCount, _ := s.db.Collection(database.ColFirewallRules).CountDocuments(ctx, bson.M{})
	blockedCount, _ := s.db.Collection(database.ColBlockedIPs).CountDocuments(ctx, bson.M{})
	status.RulesCount = int(rulesCount)
	status.BlockedIPsCount = int(blockedCount)

	// Fail2Ban status
	if result, err := agent.RunCommand(ctx, "fail2ban-client", "status"); err == nil {
		status.Fail2BanActive = true
		for _, line := range strings.Split(result.Output, "\n") {
			if strings.Contains(line, "Number of jail") {
				// Parse total bans across jails if available
			}
		}
	}

	return status, nil
}

// ListRules returns all configured firewall rules.
func (s *FirewallService) ListRules(ctx context.Context) ([]models.FirewallRule, error) {
	col := s.db.Collection(database.ColFirewallRules)
	cursor, err := col.Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var rules []models.FirewallRule
	if err := cursor.All(ctx, &rules); err != nil {
		return nil, err
	}
	if rules == nil {
		rules = []models.FirewallRule{}
	}
	return rules, nil
}

// AllowPort creates a firewall rule to allow traffic on a port.
func (s *FirewallService) AllowPort(ctx context.Context, req *models.AllowPortRequest) error {
	if err := agent.AllowPort(ctx, req.Port, req.Protocol, req.Source); err != nil {
		return fmt.Errorf("failed to allow port: %w", err)
	}

	rule := models.FirewallRule{
		Port:      req.Port,
		Protocol:  req.Protocol,
		Action:    "allow",
		Source:    req.Source,
		Comment:   req.Comment,
		CreatedAt: time.Now(),
	}
	_, err := s.db.Collection(database.ColFirewallRules).InsertOne(ctx, rule)
	return err
}

// DenyPort creates a firewall rule to deny traffic on a port.
func (s *FirewallService) DenyPort(ctx context.Context, req *models.AllowPortRequest) error {
	if err := agent.DenyPort(ctx, req.Port, req.Protocol); err != nil {
		return fmt.Errorf("failed to deny port: %w", err)
	}

	rule := models.FirewallRule{
		Port:      req.Port,
		Protocol:  req.Protocol,
		Action:    "deny",
		Source:    req.Source,
		Comment:   req.Comment,
		CreatedAt: time.Now(),
	}
	_, err := s.db.Collection(database.ColFirewallRules).InsertOne(ctx, rule)
	return err
}

// DeleteRule removes a firewall rule by its ID.
func (s *FirewallService) DeleteRule(ctx context.Context, id string) error {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return fmt.Errorf("invalid rule ID")
	}

	col := s.db.Collection(database.ColFirewallRules)
	var rule models.FirewallRule
	if err := col.FindOne(ctx, bson.M{"_id": oid}).Decode(&rule); err != nil {
		return fmt.Errorf("rule not found")
	}

	// Delete from UFW
	ufwCmd := fmt.Sprintf("ufw delete %s %s/%s", rule.Action, rule.Port, rule.Protocol)
	if rule.Source != "" {
		ufwCmd = fmt.Sprintf("ufw delete %s from %s to any port %s proto %s", rule.Action, rule.Source, rule.Port, rule.Protocol)
	}
	agent.RunCommand(ctx, "bash", "-c", ufwCmd)

	_, err = col.DeleteOne(ctx, bson.M{"_id": oid})
	return err
}

// BlockIP adds an IP address to the server's blocklist.
func (s *FirewallService) BlockIP(ctx context.Context, req *models.BlockIPRequest) error {
	if err := agent.BlockIP(ctx, req.IP); err != nil {
		return fmt.Errorf("failed to block IP: %w", err)
	}

	blocked := models.BlockedIP{
		IP:        req.IP,
		Reason:    req.Reason,
		Duration:  req.Duration,
		CreatedAt: time.Now(),
	}

	// Calculate expiry
	if req.Duration != "permanent" {
		var dur time.Duration
		switch req.Duration {
		case "1h":
			dur = time.Hour
		case "6h":
			dur = 6 * time.Hour
		case "24h":
			dur = 24 * time.Hour
		case "7d":
			dur = 7 * 24 * time.Hour
		case "30d":
			dur = 30 * 24 * time.Hour
		}
		expires := time.Now().Add(dur)
		blocked.ExpiresAt = &expires
	}

	_, err := s.db.Collection(database.ColBlockedIPs).InsertOne(ctx, blocked)
	return err
}

// UnblockIP removes an IP address from the server's blocklist.
func (s *FirewallService) UnblockIP(ctx context.Context, ip string) error {
	if err := agent.UnblockIP(ctx, ip); err != nil {
		return fmt.Errorf("failed to unblock IP: %w", err)
	}
	_, err := s.db.Collection(database.ColBlockedIPs).DeleteOne(ctx, bson.M{"ip": ip})
	return err
}

// ListBlockedIPs returns all currently blocked IP addresses.
func (s *FirewallService) ListBlockedIPs(ctx context.Context) ([]models.BlockedIP, error) {
	col := s.db.Collection(database.ColBlockedIPs)
	cursor, err := col.Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var blocked []models.BlockedIP
	if err := cursor.All(ctx, &blocked); err != nil {
		return nil, err
	}
	if blocked == nil {
		blocked = []models.BlockedIP{}
	}
	return blocked, nil
}

// Fail2BanStatus returns the current Fail2Ban status and jail information.
func (s *FirewallService) Fail2BanStatus(ctx context.Context) (map[string]interface{}, error) {
	status := make(map[string]interface{})

	result, err := agent.RunCommand(ctx, "fail2ban-client", "status")
	if err != nil {
		status["active"] = false
		return status, nil
	}
	status["active"] = true

	// Extract jail list
	var jails []string
	for _, line := range strings.Split(result.Output, "\n") {
		if strings.Contains(line, "Jail list:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				for _, j := range strings.Split(strings.TrimSpace(parts[1]), ",") {
					j = strings.TrimSpace(j)
					if j != "" {
						jails = append(jails, j)
					}
				}
			}
		}
	}

	// Get per-jail details
	var jailDetails []map[string]interface{}
	for _, jail := range jails {
		detail := map[string]interface{}{"name": jail}
		if jailResult, err := agent.RunCommand(ctx, "fail2ban-client", "status", jail); err == nil {
			for _, line := range strings.Split(jailResult.Output, "\n") {
				line = strings.TrimSpace(line)
				if strings.Contains(line, "Currently banned:") {
					parts := strings.SplitN(line, ":", 2)
					if len(parts) == 2 {
						detail["currently_banned"] = strings.TrimSpace(parts[1])
					}
				}
				if strings.Contains(line, "Total banned:") {
					parts := strings.SplitN(line, ":", 2)
					if len(parts) == 2 {
						detail["total_banned"] = strings.TrimSpace(parts[1])
					}
				}
			}
		}
		jailDetails = append(jailDetails, detail)
	}
	status["jails"] = jailDetails

	return status, nil
}

// UpdateFail2Ban updates Fail2Ban configuration for a specific jail.
func (s *FirewallService) UpdateFail2Ban(ctx context.Context, config *models.Fail2BanConfig) error {
	content := fmt.Sprintf("[%s]\nenabled = true\nbantime = %d\nmaxretry = %d\nfindtime = %d\n",
		config.Jail, config.BanTime, config.MaxRetries, config.FindTime)

	filePath := fmt.Sprintf("/etc/fail2ban/jail.d/%s.local", config.Jail)
	if _, err := agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("echo '%s' > %s", content, filePath)); err != nil {
		return fmt.Errorf("failed to write fail2ban config: %w", err)
	}

	return agent.ServiceAction(ctx, "fail2ban", "restart")
}
