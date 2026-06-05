package jwt

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	UserID string `json:"uid"`
	Email  string `json:"email"`
	gojwt.RegisteredClaims
}

type Manager struct {
	secret []byte
	access time.Duration
}

func New(secret string, accessExpiry time.Duration) *Manager {
	return &Manager{secret: []byte(secret), access: accessExpiry}
}

func (m *Manager) AccessToken(userID, email string) (string, time.Time, error) {
	now := time.Now()
	exp := now.Add(m.access)
	c := Claims{
		UserID: userID,
		Email:  email,
		RegisteredClaims: gojwt.RegisteredClaims{
			IssuedAt:  gojwt.NewNumericDate(now),
			ExpiresAt: gojwt.NewNumericDate(exp),
			Subject:   userID,
			Issuer:    "betazen-mail",
		},
	}
	t := gojwt.NewWithClaims(gojwt.SigningMethodHS256, c)
	s, err := t.SignedString(m.secret)
	return s, exp, err
}

func (m *Manager) Validate(tok string) (*Claims, error) {
	c := &Claims{}
	t, err := gojwt.ParseWithClaims(tok, c, func(t *gojwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*gojwt.SigningMethodHMAC); !ok {
			return nil, errors.New("bad signing method")
		}
		return m.secret, nil
	})
	if err != nil || !t.Valid {
		return nil, errors.New("invalid token")
	}
	return c, nil
}

func RandomRefreshToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
