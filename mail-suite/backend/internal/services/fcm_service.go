package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/betazeninfotech/mail-suite/internal/config"
	"github.com/betazeninfotech/mail-suite/internal/database"
	"github.com/betazeninfotech/mail-suite/internal/models"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const fcmScope = "https://www.googleapis.com/auth/firebase.messaging"

// FCMService sends push notifications to the Flutter app's registered devices
// via the Firebase Cloud Messaging HTTP v1 API. It's a no-op sender when no
// service-account credentials are configured (Enabled() == false), so the rest
// of the app runs fine without mobile push set up.
type FCMService struct {
	db        *database.DB
	projectID string
	ts        oauth2.TokenSource
	client    *http.Client
	enabled   bool
}

func NewFCMService(db *database.DB, cfg *config.Config) *FCMService {
	s := &FCMService{db: db, client: &http.Client{Timeout: 15 * time.Second}}
	if cfg.FCMCredentialsFile == "" {
		return s // disabled — no credentials
	}
	data, err := os.ReadFile(cfg.FCMCredentialsFile)
	if err != nil {
		// Not an error condition — most servers simply haven't set up mobile push
		// yet. Point the operator at how to enable it.
		log.Info().Str("file", cfg.FCMCredentialsFile).
			Msg("mobile push (FCM) disabled — drop the Firebase service-account JSON at this path to enable")
		return s
	}
	creds, err := google.CredentialsFromJSON(context.Background(), data, fcmScope)
	if err != nil {
		log.Warn().Err(err).Msg("FCM credentials invalid; mobile push disabled")
		return s
	}
	s.projectID = cfg.FCMProjectID
	if s.projectID == "" {
		s.projectID = creds.ProjectID
	}
	if s.projectID == "" {
		log.Warn().Msg("FCM project id not resolvable; mobile push disabled")
		return s
	}
	s.ts = creds.TokenSource
	s.enabled = true
	log.Info().Str("project", s.projectID).Msg("FCM mobile push enabled")
	return s
}

func (s *FCMService) Enabled() bool { return s.enabled }

// SendToUser delivers a notification to every registered device of the user and
// returns how many FCM accepted. Dead tokens (UNREGISTERED/NOT_FOUND) are pruned
// so we stop trying them. Best-effort — failures are logged, never returned.
func (s *FCMService) SendToUser(ctx context.Context, userID primitive.ObjectID, title, body, url string) int {
	if !s.enabled {
		return 0
	}
	cur, err := s.db.Col(database.ColDevices).Find(ctx, bson.M{"user_id": userID})
	if err != nil {
		return 0
	}
	var devices []models.Device
	if err := cur.All(ctx, &devices); err != nil {
		return 0
	}
	sent := 0
	for i := range devices {
		if devices[i].FCMToken == "" {
			continue
		}
		if s.sendOne(ctx, &devices[i], title, body, url) {
			sent++
		}
	}
	return sent
}

func (s *FCMService) sendOne(ctx context.Context, d *models.Device, title, body, url string) bool {
	msg := map[string]any{
		"message": map[string]any{
			"token":        d.FCMToken,
			"notification": map[string]string{"title": title, "body": body},
			"data":         map[string]string{"url": url},
			"android": map[string]any{
				"priority":     "high",
				"notification": map[string]string{"channel_id": "mail_default"},
			},
		},
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return false
	}
	tok, err := s.ts.Token()
	if err != nil {
		log.Warn().Err(err).Msg("FCM oauth token failed")
		return false
	}
	endpoint := fmt.Sprintf("https://fcm.googleapis.com/v1/projects/%s/messages:send", s.projectID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		log.Warn().Err(err).Msg("FCM send failed")
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return true
	}
	respBody := readClip(resp.Body, 4096)
	// A dead/rotated token → prune so we stop pushing to it.
	if resp.StatusCode == http.StatusNotFound ||
		strings.Contains(respBody, "UNREGISTERED") ||
		strings.Contains(respBody, "NOT_FOUND") ||
		strings.Contains(respBody, "InvalidRegistration") {
		_, _ = s.db.Col(database.ColDevices).DeleteOne(ctx, bson.M{"_id": d.ID})
		log.Info().Str("model", d.Model).Msg("pruned dead FCM device token")
		return false
	}
	log.Warn().Int("status", resp.StatusCode).Str("body", clip(respBody, 200)).Msg("FCM non-200")
	return false
}

func readClip(r io.Reader, n int64) string {
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(io.LimitReader(r, n))
	return buf.String()
}
