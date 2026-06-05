package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

// MailSuiteService is the panel-side companion for the separate
// mail-suite/ product. It stores registered deployments (URL +
// service token) and proxies "Enable Mail" + "Status" calls to the
// mail-suite backend on behalf of the WHM operator.
//
// This does NOT replace the existing /api/v1/cpanel/email/* or
// Roundcube SSO surfaces — those stay as they are. This is purely a
// management surface for the new product.
type MailSuiteService struct {
	db   *mongo.Database
	http *http.Client
}

var ErrMailSuiteNotConfigured = errors.New("no mail-suite deployment registered for this scope")

func NewMailSuiteService(db *mongo.Database) *MailSuiteService {
	return &MailSuiteService{
		db:   db,
		http: &http.Client{Timeout: 20 * time.Second},
	}
}

func (s *MailSuiteService) Register(ctx context.Context, req models.RegisterMailSuiteRequest) (*models.MailSuiteDeployment, error) {
	d := models.MailSuiteDeployment{
		Label:        strings.TrimSpace(req.Label),
		URL:          strings.TrimRight(strings.TrimSpace(req.URL), "/"),
		ServiceToken: req.ServiceToken,
		WebmailURL:   strings.TrimRight(strings.TrimSpace(req.WebmailURL), "/"),
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if d.WebmailURL == "" {
		d.WebmailURL = d.URL + "/mail/"
	}
	res, err := s.db.Collection(database.ColMailSuiteDeployments).InsertOne(ctx, d)
	if err != nil {
		return nil, err
	}
	d.ID = res.InsertedID.(primitive.ObjectID)
	return &d, nil
}

func (s *MailSuiteService) List(ctx context.Context) ([]models.MailSuiteDeployment, error) {
	cur, err := s.db.Collection(database.ColMailSuiteDeployments).Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	out := []models.MailSuiteDeployment{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *MailSuiteService) Delete(ctx context.Context, id primitive.ObjectID) error {
	_, err := s.db.Collection(database.ColMailSuiteDeployments).DeleteOne(ctx, bson.M{"_id": id})
	return err
}

// active returns the most recently registered deployment. Future
// work scopes by vendor_id; for now, single-tenant or
// "first-found-wins" is fine.
func (s *MailSuiteService) active(ctx context.Context) (*models.MailSuiteDeployment, error) {
	cur, err := s.db.Collection(database.ColMailSuiteDeployments).Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var newest *models.MailSuiteDeployment
	for cur.Next(ctx) {
		var d models.MailSuiteDeployment
		if err := cur.Decode(&d); err != nil {
			continue
		}
		if newest == nil || d.CreatedAt.After(newest.CreatedAt) {
			cp := d
			newest = &cp
		}
	}
	if newest == nil {
		return nil, ErrMailSuiteNotConfigured
	}
	return newest, nil
}

// EnableMail proxies POST /api/v1/dns/:domain/enable-mail.
func (s *MailSuiteService) EnableMail(ctx context.Context, domain string) (map[string]interface{}, error) {
	d, err := s.active(ctx)
	if err != nil {
		return nil, err
	}
	return s.proxy(ctx, http.MethodPost, d, fmt.Sprintf("/api/v1/dns/%s/enable-mail", url.PathEscape(domain)), nil)
}

// Status proxies GET /api/v1/dns/:domain/status.
func (s *MailSuiteService) Status(ctx context.Context, domain string) (map[string]interface{}, error) {
	d, err := s.active(ctx)
	if err != nil {
		return nil, err
	}
	return s.proxy(ctx, http.MethodGet, d, fmt.Sprintf("/api/v1/dns/%s/status", url.PathEscape(domain)), nil)
}

// WebmailURL returns the configured webmail base URL for the active deployment.
func (s *MailSuiteService) WebmailURL(ctx context.Context) (string, error) {
	d, err := s.active(ctx)
	if err != nil {
		return "", err
	}
	return d.WebmailURL, nil
}

func (s *MailSuiteService) proxy(ctx context.Context, method string, d *models.MailSuiteDeployment, path string, body interface{}) (map[string]interface{}, error) {
	var buf io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		buf = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, d.URL+path, buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+d.ServiceToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	out := map[string]interface{}{}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, err
	}
	if res.StatusCode >= 300 {
		msg, _ := out["error"].(string)
		return nil, fmt.Errorf("mail-suite %s %s: %d: %s", method, path, res.StatusCode, msg)
	}
	return out, nil
}
