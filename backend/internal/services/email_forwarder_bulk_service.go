// Package services — email_forwarder_bulk_service.go.
//
// Mirrors email_bulk_service.go for the forwarder surface. Three flows:
//
//   1. Bulk Upload — CSV / XLSX with one row per forwarder. The
//      `destinations` column accepts a comma- or semicolon-separated
//      list, parsed into the []string Mongo + Postfix expect.
//      Idempotent on duplicate `source` (overwrites with the new
//      destinations list, same semantics as cPanel "edit forwarder").
//
//   2. Bulk Export — CSV / XLSX dump of forwarder rows. NO OTP gate
//      because destination addresses are visible in the same panel
//      page already; the bulk download is a convenience, not an
//      escalation surface.
//
//   3. Bulk Delete — OTP-gated multi-forwarder removal, identical
//      flow to the mailbox version. Uses ColBulkForwarderOTP so the
//      mailbox + forwarder OTP retention can be tuned independently.
//
// Tenant scope flows through CallerScope just like the mailbox flow:
// vendor-scoped callers can only touch forwarders on domains they
// own; vendor_owner sees everything.
package services

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/mailer"
	"github.com/rs/zerolog/log"
	"github.com/xuri/excelize/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

// ─────────────────────────────────────────────────────────────────────
// Templates
// ─────────────────────────────────────────────────────────────────────

func forwarderTemplateHeaders(omitUser bool) []string {
	if omitUser {
		// cPanel surface — vendor-scoped. Domain auto-derived from
		// the @part of `source`; row's owner auto-resolved from the
		// matching Domain row.
		return []string{"source", "destinations", "keep_copy"}
	}
	return []string{"source", "destinations", "keep_copy", "user"}
}

// BulkForwarderUploadCSVTemplate / …XLSXTemplate / …Name() —
// shape mirrors the mailbox helpers so a frontend that already
// downloads + uploads CSV/XLSX templates needs only a route swap.
func BulkForwarderUploadCSVTemplate(omitUser bool) []byte {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	w.Write(forwarderTemplateHeaders(omitUser))
	if omitUser {
		w.Write([]string{"sales@example.com", "alice@example.com, bob@example.com", "true"})
		w.Write([]string{"info@example.com", "team@example.com", "false"})
	} else {
		w.Write([]string{"sales@example.com", "alice@example.com, bob@example.com", "true", ""})
		w.Write([]string{"info@example.com", "team@example.com", "false", ""})
		// Row 3 — WHM admin can pick the owner for an out-of-tenant
		// row by stamping the `user` column. Mirrors the mailbox
		// template's auto-create-domain hook so admins can drive
		// onboarding via a single CSV.
		w.Write([]string{"hello@newshop.com", "founder@founder.com", "true", "alice"})
	}
	w.Flush()
	return buf.Bytes()
}

func BulkForwarderUploadXLSXTemplate(omitUser bool) ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()
	const sheet = "Forwarders"
	f.SetSheetName("Sheet1", sheet)

	headers := forwarderTemplateHeaders(omitUser)
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sheet, cell, h)
	}
	if omitUser {
		f.SetCellValue(sheet, "A2", "sales@example.com")
		f.SetCellValue(sheet, "B2", "alice@example.com, bob@example.com")
		f.SetCellValue(sheet, "C2", true)
		f.SetCellValue(sheet, "A3", "info@example.com")
		f.SetCellValue(sheet, "B3", "team@example.com")
		f.SetCellValue(sheet, "C3", false)
	} else {
		f.SetCellValue(sheet, "A2", "sales@example.com")
		f.SetCellValue(sheet, "B2", "alice@example.com, bob@example.com")
		f.SetCellValue(sheet, "C2", true)
		f.SetCellValue(sheet, "D2", "")
		f.SetCellValue(sheet, "A3", "info@example.com")
		f.SetCellValue(sheet, "B3", "team@example.com")
		f.SetCellValue(sheet, "C3", false)
		f.SetCellValue(sheet, "D3", "")
		f.SetCellValue(sheet, "A4", "hello@newshop.com")
		f.SetCellValue(sheet, "B4", "founder@founder.com")
		f.SetCellValue(sheet, "C4", true)
		f.SetCellValue(sheet, "D4", "alice")
	}

	style, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#E8EEF7"}},
		Alignment: &excelize.Alignment{Vertical: "center"},
	})
	endCell, _ := excelize.CoordinatesToCellName(len(headers), 1)
	f.SetCellStyle(sheet, "A1", endCell, style)
	f.SetPanes(sheet, &excelize.Panes{Freeze: true, Split: false, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})

	instr := "Instructions"
	f.NewSheet(instr)
	f.SetCellValue(instr, "A1", "Bulk Email Forwarder Upload Template")
	f.SetCellValue(instr, "A3", "Required columns: source, destinations")
	f.SetCellValue(instr, "A4", "destinations: comma- OR semicolon-separated list of full email addresses. Example: alice@x.com, bob@y.com")
	f.SetCellValue(instr, "A5", "Optional column: keep_copy (true/false) — when true, a copy of every forwarded message also lands in the source mailbox. Default: false.")
	if omitUser {
		f.SetCellValue(instr, "A6", "Domain auto-derived: the @part of `source` determines which domain the forwarder belongs to. The domain MUST already exist under your account.")
		f.SetCellValue(instr, "A7", "Tenant scope: rows whose source belongs to a domain you don't own fail with a clear error.")
	} else {
		f.SetCellValue(instr, "A6", "Optional column: user — when the source's domain doesn't exist yet, set this to the owning vendor's username and the bulk uploader will provision the domain (PHP 8.2 default) before creating the forwarder.")
	}
	f.SetCellValue(instr, "A9", "Idempotent: if a forwarder for the same `source` already exists, this upload OVERWRITES its destinations list (cPanel-style edit). Re-running the same file twice is a no-op.")
	f.SetCellValue(instr, "A10", "Server transfer: this file format is identical across panel installs — export from one box, import to another.")

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, fmt.Errorf("write template xlsx: %w", err)
	}
	return buf.Bytes(), nil
}

func BulkForwarderUploadCSVTemplateName() string  { return "forwarders-template.csv" }
func BulkForwarderUploadXLSXTemplateName() string { return "forwarders-template.xlsx" }

// ─────────────────────────────────────────────────────────────────────
// Upload
// ─────────────────────────────────────────────────────────────────────

type BulkForwarderUploadRow struct {
	RowNumber    int      `json:"row"`
	Source       string   `json:"source"`
	Destinations []string `json:"destinations"`
	KeepCopy     bool     `json:"keep_copy"`
	Owner        string   `json:"owner,omitempty"`
	Updated      bool     `json:"updated,omitempty"` // overwrote existing row
	Success      bool     `json:"success"`
	Error        string   `json:"error,omitempty"`
}

type BulkForwarderUploadResponse struct {
	Format         string                   `json:"format"`
	TotalRows      int                      `json:"total_rows"`
	Successes      int                      `json:"successes"`
	Failures       int                      `json:"failures"`
	Updates        int                      `json:"updates"`
	DomainsCreated int                      `json:"domains_created"`
	Items          []BulkForwarderUploadRow `json:"items"`
}

func (s *EmailService) BulkUploadForwardersFromContentType(
	ctx context.Context, body io.Reader, contentType, filename string,
) (*BulkForwarderUploadResponse, error) {
	// Detect by Content-Type first, fall back to filename suffix —
	// some browsers send application/octet-stream for .xlsx uploads
	// from File Manager-style dialogs.
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if strings.Contains(ct, "spreadsheetml") || strings.HasSuffix(strings.ToLower(filename), ".xlsx") {
		return s.bulkUploadForwardersXLSX(ctx, body)
	}
	return s.bulkUploadForwardersCSV(ctx, body)
}

func (s *EmailService) bulkUploadForwardersCSV(ctx context.Context, body io.Reader) (*BulkForwarderUploadResponse, error) {
	r := csv.NewReader(body)
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse csv: %w", err)
	}
	return s.executeForwarderRows(ctx, "csv", rows)
}

func (s *EmailService) bulkUploadForwardersXLSX(ctx context.Context, body io.Reader) (*BulkForwarderUploadResponse, error) {
	f, err := excelize.OpenReader(body)
	if err != nil {
		return nil, fmt.Errorf("open xlsx: %w", err)
	}
	defer f.Close()
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, fmt.Errorf("xlsx has no sheets")
	}
	rows, err := f.GetRows(sheets[0])
	if err != nil {
		return nil, fmt.Errorf("read xlsx rows: %w", err)
	}
	return s.executeForwarderRows(ctx, "xlsx", rows)
}

// executeForwarderRows is the shared upload loop. Header row is row 0;
// every subsequent row goes through createOrUpdateForwarderRow and the
// per-row outcome is collected so the operator gets a result table
// instead of a fail-fast error from the first bad row. After the loop
// we run postmap + reload ONCE — not per-row — so a 100-forwarder
// upload doesn't reload Postfix 100 times.
func (s *EmailService) executeForwarderRows(ctx context.Context, format string, rows [][]string) (*BulkForwarderUploadResponse, error) {
	if len(rows) < 2 {
		return &BulkForwarderUploadResponse{Format: format}, nil
	}
	headers := rows[0]
	idx := buildForwarderHeaderIndex(headers)
	if idx.source < 0 || idx.destinations < 0 {
		return nil, fmt.Errorf("template missing required columns: source, destinations")
	}
	res := &BulkForwarderUploadResponse{Format: format, Items: make([]BulkForwarderUploadRow, 0, len(rows)-1)}

	// Whether the caller is a tenant-scoped vendor (cpanel) or a
	// platform owner (whm). When tenant-scoped, the `user` column on
	// each row is IGNORED (caller can only act on their own domains).
	scope := GetCallerScope(ctx)
	isTenantScoped := scope != nil && scope.Role != "vendor_owner"

	for i, row := range rows[1:] {
		rowNum := i + 2 // 1-indexed + header
		if rowAllBlank(row) {
			continue
		}
		res.TotalRows++
		out := s.createOrUpdateForwarderRow(ctx, idx, row, isTenantScoped)
		out.RowNumber = rowNum
		if out.Success {
			res.Successes++
			if out.Updated {
				res.Updates++
			}
		} else {
			res.Failures++
		}
		res.Items = append(res.Items, out)
	}

	// Single postmap + reload at the end (best effort).
	if res.Successes > 0 {
		if err := s.PostfixApplyForwardersFlush(ctx); err != nil {
			log.Warn().Err(err).Msg("bulk-forwarder upload: postfix flush failed — rows landed in Mongo, run `bzpanel heal-forwarders` to reconcile")
		}
	}
	return res, nil
}

type forwarderHeaderIndex struct {
	source       int
	destinations int
	keepCopy     int
	user         int
}

func buildForwarderHeaderIndex(headers []string) forwarderHeaderIndex {
	idx := forwarderHeaderIndex{source: -1, destinations: -1, keepCopy: -1, user: -1}
	for i, h := range headers {
		switch normaliseHeader(h) {
		case "source", "email", "alias", "from":
			idx.source = i
		case "destinations", "destination", "to", "forward_to", "forwardto":
			idx.destinations = i
		case "keep_copy", "keepcopy", "keep", "copy", "store_copy":
			idx.keepCopy = i
		case "user", "owner", "username":
			idx.user = i
		}
	}
	return idx
}

// createOrUpdateForwarderRow processes one parsed CSV/XLSX row.
//
// Tenant scoping: a tenant-scoped caller (cpanel surface) silently
// ignores the row's `user` column (server force-overrides via the
// caller's CallerScope), and the row's source domain MUST be one of
// the caller's tenant's domains — `cs.AssertOwnsDomain` enforces.
//
// Idempotent: if a forwarder for the same source already exists, the
// destinations + keep_copy fields are overwritten (cPanel "edit"
// semantics). The Updated flag in the result row tells the operator
// which rows replaced versus newly created.
func (s *EmailService) createOrUpdateForwarderRow(ctx context.Context, idx forwarderHeaderIndex, row []string, isTenantScoped bool) BulkForwarderUploadRow {
	out := BulkForwarderUploadRow{}
	source := ""
	if idx.source < len(row) {
		source = strings.ToLower(strings.TrimSpace(row[idx.source]))
	}
	if source == "" || !strings.Contains(source, "@") {
		out.Error = "source is required and must be a full email address"
		return out
	}
	out.Source = source
	parts := strings.SplitN(source, "@", 2)
	domain := strings.ToLower(strings.TrimSpace(parts[1]))

	// Tenant ownership check first — cheaper than parsing destinations
	// for a row that's going to fail anyway.
	scope := GetCallerScope(ctx)
	if isTenantScoped && scope != nil {
		if err := scope.AssertOwnsDomain(ctx, s.db, domain); err != nil {
			out.Error = fmt.Sprintf("domain %s is not in your tenant", domain)
			return out
		}
	}

	rawDest := ""
	if idx.destinations < len(row) {
		rawDest = strings.TrimSpace(row[idx.destinations])
	}
	dests := parseForwarderDestinations(rawDest)
	if len(dests) == 0 {
		out.Error = "destinations is required (comma- or semicolon-separated list of full email addresses)"
		return out
	}
	for _, d := range dests {
		if !strings.Contains(d, "@") {
			out.Error = fmt.Sprintf("destination %q is not a full email address", d)
			return out
		}
	}
	out.Destinations = dests

	keep := false
	if idx.keepCopy >= 0 && idx.keepCopy < len(row) {
		keep = parseBoolCell(row[idx.keepCopy])
	}
	out.KeepCopy = keep

	if !isTenantScoped && idx.user >= 0 && idx.user < len(row) {
		out.Owner = strings.TrimSpace(row[idx.user])
	}

	// Find-or-overwrite. Same source is treated as an UPDATE so a
	// re-uploaded CSV is a no-op rather than a unique-index violation.
	col := s.db.Collection(database.ColForwarders)
	var existing models.EmailForwarder
	err := col.FindOne(ctx, bson.M{"source": source}).Decode(&existing)
	if err == nil {
		// Overwrite destinations + keep_copy on the existing row, then
		// reapply Postfix line.
		_, err = col.UpdateByID(ctx, existing.ID, bson.M{"$set": bson.M{
			"destinations": dests,
			"keep_copy":    keep,
			"domain":       domain,
		}})
		if err != nil {
			out.Error = fmt.Sprintf("update existing forwarder: %v", err)
			return out
		}
		if err := s.applyForwarderToPostfix(ctx, source, dests, false); err != nil {
			log.Warn().Err(err).Str("source", source).Msg("bulk-forwarder: postfix apply failed on update")
		}
		out.Updated = true
		out.Success = true
		return out
	} else if err != mongo.ErrNoDocuments {
		out.Error = fmt.Sprintf("look up existing forwarder: %v", err)
		return out
	}

	// Fresh insert. Use the existing service method so webhook fan-out
	// fires the same shape as a single-row create — but skip the
	// per-row postfix reload by going through applyForwarderToPostfix
	// with reload=false, then doing the Mongo write inline.
	if err := s.applyForwarderToPostfix(ctx, source, dests, false); err != nil {
		log.Warn().Err(err).Str("source", source).Msg("bulk-forwarder: postfix apply failed on create")
	}
	doc := models.EmailForwarder{
		Source:       source,
		Destinations: dests,
		KeepCopy:     keep,
		Domain:       domain,
		CreatedAt:    time.Now(),
	}
	insRes, err := col.InsertOne(ctx, doc)
	if err != nil {
		out.Error = fmt.Sprintf("insert forwarder: %v", err)
		return out
	}
	doc.ID = insRes.InsertedID.(primitive.ObjectID)
	EmitEvent(ctx, "email.forwarder.created", LookupTenantIDForDomain(ctx, s.db, domain), map[string]any{
		"id":           doc.ID.Hex(),
		"source":       source,
		"destinations": dests,
		"domain":       domain,
	})
	out.Success = true
	return out
}

// parseForwarderDestinations splits a destinations cell on `,` or `;`,
// trims, lower-cases, and de-dupes the result. Empty entries are
// dropped silently so a trailing comma doesn't produce an empty
// destination address.
func parseForwarderDestinations(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	// Allow EITHER comma OR semicolon as separator (Outlook copy-
	// paste defaults to `;`).
	splitter := func(r rune) bool { return r == ',' || r == ';' || r == '\n' }
	parts := strings.FieldsFunc(raw, splitter)
	seen := make(map[string]struct{}, len(parts))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		v := strings.ToLower(strings.TrimSpace(p))
		if v == "" {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// parseBoolCell handles the variety of "truthy" cell values an
// operator might type — "true", "TRUE", "yes", "y", "1" — without
// throwing. Anything else (including "" and "0") is false.
func parseBoolCell(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "y", "1", "t":
		return true
	}
	return false
}

// ─────────────────────────────────────────────────────────────────────
// Export
// ─────────────────────────────────────────────────────────────────────

type ExportableForwarder struct {
	ID           string   `json:"id"`
	Source       string   `json:"source"`
	Domain       string   `json:"domain"`
	Destinations []string `json:"destinations"`
	KeepCopy     bool     `json:"keep_copy"`
	CreatedAt    string   `json:"created_at"`
}

func (s *EmailService) FetchForwardersForExport(ctx context.Context, ids []string, listAll bool) ([]ExportableForwarder, error) {
	col := s.db.Collection(database.ColForwarders)
	filter := bson.M{}
	if !listAll {
		if len(ids) == 0 {
			return []ExportableForwarder{}, nil
		}
		oids := make([]primitive.ObjectID, 0, len(ids))
		for _, id := range ids {
			oid, err := primitive.ObjectIDFromHex(strings.TrimSpace(id))
			if err == nil {
				oids = append(oids, oid)
			}
		}
		if len(oids) == 0 {
			return []ExportableForwarder{}, nil
		}
		filter["_id"] = bson.M{"$in": oids}
	}
	if scope := GetCallerScope(ctx); scope != nil {
		filter = scope.ApplyDomainScope(ctx, s.db, "domain", filter)
	}
	cursor, err := col.Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var fwds []models.EmailForwarder
	if err := cursor.All(ctx, &fwds); err != nil {
		return nil, err
	}
	out := make([]ExportableForwarder, 0, len(fwds))
	for _, f := range fwds {
		out = append(out, ExportableForwarder{
			ID:           f.ID.Hex(),
			Source:       f.Source,
			Domain:       f.Domain,
			Destinations: f.Destinations,
			KeepCopy:     f.KeepCopy,
			CreatedAt:    f.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out, nil
}

func ExportForwardersCSV(rows []ExportableForwarder) []byte {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	w.Write([]string{"source", "domain", "destinations", "keep_copy", "created_at"})
	for _, r := range rows {
		w.Write([]string{
			r.Source,
			r.Domain,
			strings.Join(r.Destinations, ", "),
			fmt.Sprintf("%t", r.KeepCopy),
			r.CreatedAt,
		})
	}
	w.Flush()
	return buf.Bytes()
}

func ExportForwardersXLSX(rows []ExportableForwarder) ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()
	const sheet = "Forwarders"
	f.SetSheetName("Sheet1", sheet)
	headers := []string{"source", "domain", "destinations", "keep_copy", "created_at"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sheet, cell, h)
	}
	for ri, r := range rows {
		row := ri + 2
		f.SetCellValue(sheet, fmt.Sprintf("A%d", row), r.Source)
		f.SetCellValue(sheet, fmt.Sprintf("B%d", row), r.Domain)
		f.SetCellValue(sheet, fmt.Sprintf("C%d", row), strings.Join(r.Destinations, ", "))
		f.SetCellValue(sheet, fmt.Sprintf("D%d", row), r.KeepCopy)
		f.SetCellValue(sheet, fmt.Sprintf("E%d", row), r.CreatedAt)
	}
	style, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}, Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#E8EEF7"}}})
	endCell, _ := excelize.CoordinatesToCellName(len(headers), 1)
	f.SetCellStyle(sheet, "A1", endCell, style)
	f.SetPanes(sheet, &excelize.Panes{Freeze: true, Split: false, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func ForwarderExportFilenameCSV() string {
	return fmt.Sprintf("forwarders-%s.csv", time.Now().Format("20060102-150405"))
}
func ForwarderExportFilenameXLSX() string {
	return fmt.Sprintf("forwarders-%s.xlsx", time.Now().Format("20060102-150405"))
}

// ─────────────────────────────────────────────────────────────────────
// Bulk Delete (OTP-gated)
// ─────────────────────────────────────────────────────────────────────

const bulkForwarderOTPTTL = 10 * time.Minute
const BulkForwarderOTPMaxAttempts = 5

type BulkForwarderOTPRequest struct {
	ID            primitive.ObjectID   `bson:"_id,omitempty"`
	Token         string               `bson:"token"`
	UserID        primitive.ObjectID   `bson:"user_id"`
	Email         string               `bson:"email"`
	ForwarderIDs  []primitive.ObjectID `bson:"forwarder_ids"`
	Sources       []string             `bson:"sources"`
	CodeHash      string               `bson:"code_hash"`
	Attempts      int                  `bson:"attempts"`
	Used          bool                 `bson:"used"`
	CreatedAt     time.Time            `bson:"created_at"`
	ExpiresAt     time.Time            `bson:"expires_at"`
}

type BulkForwarderOTPRequestResult struct {
	Token          string    `json:"token"`
	Email          string    `json:"email"`
	ForwarderCount int       `json:"forwarder_count"`
	Sources        []string  `json:"sources"`
	ExpiresAt      time.Time `json:"expires_at"`
	MailerEnabled  bool      `json:"mailer_enabled"`
}

type BulkForwarderDeleteRowResult struct {
	ID      string `json:"id"`
	Source  string `json:"source"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

type BulkForwarderDeleteConfirmResult struct {
	TotalRows int                            `json:"total_rows"`
	Successes int                            `json:"successes"`
	Failures  int                            `json:"failures"`
	Items     []BulkForwarderDeleteRowResult `json:"items"`
}

func (s *EmailService) RequestBulkForwarderDeleteOTP(ctx context.Context, userID primitive.ObjectID, ids []string) (*BulkForwarderOTPRequestResult, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("no forwarder ids supplied")
	}
	if len(ids) > 500 {
		return nil, fmt.Errorf("too many ids (cap 500)")
	}

	// Resolve admin (caller) for the email recipient + audit fields.
	var admin models.User
	if err := s.db.Collection(database.ColUsers).FindOne(ctx, bson.M{"_id": userID}).Decode(&admin); err != nil {
		return nil, fmt.Errorf("look up admin: %w", err)
	}

	// Resolve the requested ids → ObjectIDs + sources, dropping any
	// that don't exist (so the OTP doc carries only valid targets).
	col := s.db.Collection(database.ColForwarders)
	resolvedIDs := make([]primitive.ObjectID, 0, len(ids))
	resolvedSrcs := make([]string, 0, len(ids))
	for _, id := range ids {
		oid, err := primitive.ObjectIDFromHex(strings.TrimSpace(id))
		if err != nil {
			continue
		}
		var fwd models.EmailForwarder
		if err := col.FindOne(ctx, bson.M{"_id": oid}).Decode(&fwd); err != nil {
			continue
		}
		resolvedIDs = append(resolvedIDs, fwd.ID)
		resolvedSrcs = append(resolvedSrcs, fwd.Source)
	}
	if len(resolvedIDs) == 0 {
		return nil, fmt.Errorf("none of the selected ids matched a forwarder row")
	}

	code, err := generateBulkDeleteCode()
	if err != nil {
		return nil, fmt.Errorf("generate code: %w", err)
	}
	token, err := generateBulkDeleteToken()
	if err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}
	now := time.Now()
	doc := BulkForwarderOTPRequest{
		Token:        token,
		UserID:       userID,
		Email:        strings.ToLower(strings.TrimSpace(admin.Email)),
		ForwarderIDs: resolvedIDs,
		Sources:      resolvedSrcs,
		CodeHash:     bulkDeleteSHA256Hex(code),
		Attempts:     0,
		Used:         false,
		CreatedAt:    now,
		ExpiresAt:    now.Add(bulkForwarderOTPTTL),
	}
	if _, err := s.db.Collection(database.ColBulkForwarderOTP).InsertOne(ctx, doc); err != nil {
		return nil, fmt.Errorf("save otp request: %w", err)
	}

	mailerEnabled := s.mailer != nil && s.mailer.Enabled()
	if !mailerEnabled {
		log.Warn().Str("admin_email", admin.Email).Msg("bulk-forwarder OTP: SMTP not configured — code only in stderr")
		fmt.Printf("[bulk-forwarder] OTP for %s (mailer disabled): %s — forwarders=%d, expires in %dm\n",
			admin.Email, code, len(resolvedSrcs), int(bulkForwarderOTPTTL/time.Minute))
	} else {
		subject := fmt.Sprintf("Confirm bulk forwarder delete — %d forwarder(s)", len(resolvedSrcs))
		text := fmt.Sprintf(
			"Hi %s,\n\nYou requested a bulk delete of %d email forwarder(s) on the panel.\n"+
				"Your confirmation code is: %s\n\n"+
				"This code expires in %d minutes. If you did NOT request this, ignore this email — the action will not run.\n",
			displayName(admin), len(resolvedSrcs), code, int(bulkForwarderOTPTTL/time.Minute),
		)
		htmlBody := fmt.Sprintf(
			"<p>Hi %s,</p><p>You requested a bulk delete of <b>%d</b> email forwarder(s) on the panel.</p>"+
				"<p>Your confirmation code is:</p><p style=\"font-size:24px;letter-spacing:4px;font-weight:bold;\">%s</p>"+
				"<p>This code expires in %d minutes. If you did NOT request this, ignore this email — the action will not run.</p>",
			escapeHTML(displayName(admin)), len(resolvedSrcs), code, int(bulkForwarderOTPTTL/time.Minute),
		)
		if err := s.mailer.Send(ctx, mailer.Message{To: admin.Email, Subject: subject, Text: text, HTML: htmlBody}); err != nil {
			log.Error().Err(err).Str("admin_email", admin.Email).Msg("bulk-forwarder OTP: send failed")
			return nil, fmt.Errorf("send otp email: %w", err)
		}
	}

	return &BulkForwarderOTPRequestResult{
		Token:          token,
		Email:          admin.Email,
		ForwarderCount: len(resolvedSrcs),
		Sources:        resolvedSrcs,
		ExpiresAt:      doc.ExpiresAt,
		MailerEnabled:  mailerEnabled,
	}, nil
}

// ConfirmBulkForwarderDelete validates the OTP and runs DeleteForwarder
// per id. Mark used BEFORE destructive work so a concurrent retry can't
// fire DeleteForwarder twice.
func (s *EmailService) ConfirmBulkForwarderDelete(ctx context.Context, userID primitive.ObjectID, token, code string) (*BulkForwarderDeleteConfirmResult, error) {
	col := s.db.Collection(database.ColBulkForwarderOTP)
	var doc BulkForwarderOTPRequest
	if err := col.FindOne(ctx, bson.M{"token": token}).Decode(&doc); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("invalid or expired confirmation token")
		}
		return nil, fmt.Errorf("look up otp: %w", err)
	}
	if doc.Used {
		return nil, fmt.Errorf("this confirmation has already been used")
	}
	if time.Now().After(doc.ExpiresAt) {
		return nil, fmt.Errorf("confirmation expired — request a new code")
	}
	if doc.UserID != userID {
		return nil, fmt.Errorf("this confirmation belongs to a different account")
	}
	expected := bulkDeleteSHA256Hex(strings.TrimSpace(code))
	if expected != doc.CodeHash {
		newAttempts := doc.Attempts + 1
		update := bson.M{"$set": bson.M{"attempts": newAttempts}}
		if newAttempts >= BulkForwarderOTPMaxAttempts {
			update["$set"].(bson.M)["used"] = true
		}
		_, _ = col.UpdateByID(ctx, doc.ID, update)
		if newAttempts >= BulkForwarderOTPMaxAttempts {
			return nil, fmt.Errorf("too many wrong codes — request a new one")
		}
		return nil, fmt.Errorf("wrong code (%d attempts left)", BulkForwarderOTPMaxAttempts-newAttempts)
	}
	if _, err := col.UpdateByID(ctx, doc.ID, bson.M{"$set": bson.M{"used": true}}); err != nil {
		return nil, fmt.Errorf("mark otp used: %w", err)
	}

	res := &BulkForwarderDeleteConfirmResult{
		TotalRows: len(doc.ForwarderIDs),
		Items:     make([]BulkForwarderDeleteRowResult, 0, len(doc.ForwarderIDs)),
	}
	srcByID := map[string]string{}
	for i, id := range doc.ForwarderIDs {
		if i < len(doc.Sources) {
			srcByID[id.Hex()] = doc.Sources[i]
		}
	}
	for _, id := range doc.ForwarderIDs {
		hex := id.Hex()
		row := BulkForwarderDeleteRowResult{ID: hex, Source: srcByID[hex]}
		if err := s.DeleteForwarder(ctx, hex); err != nil {
			row.Success = false
			row.Error = err.Error()
			res.Failures++
		} else {
			row.Success = true
			res.Successes++
		}
		res.Items = append(res.Items, row)
	}
	log.Info().Str("admin_email", doc.Email).Int("requested", len(doc.ForwarderIDs)).Int("succeeded", res.Successes).Int("failed", res.Failures).Msg("bulk-forwarder-delete: confirmed + executed")
	return res, nil
}

// displayName falls back through Name → Username → Email so OTP
// emails never read "Hi ,".
func displayName(u models.User) string {
	if n := strings.TrimSpace(u.Name); n != "" {
		return n
	}
	if n := strings.TrimSpace(u.Username); n != "" {
		return n
	}
	return u.Email
}
