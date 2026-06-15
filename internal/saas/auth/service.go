package auth

import (
	"errors"
	"fmt"
	"time"

	"quantsaas/internal/saas/config"

	"github.com/golang-jwt/jwt/v5"
)

type Service struct {
	secret []byte
	issuer string
	ttl    time.Duration
}

type Claims struct {
	UserID uint   `json:"user_id"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

func NewService(cfg config.JWTConfig) (*Service, error) {
	if cfg.Secret == "" {
		return nil, errors.New("jwt secret is required")
	}
	ttl := time.Duration(cfg.TTLMinutes) * time.Minute
	if ttl <= 0 {
		return nil, errors.New("jwt ttl must be positive")
	}
	return &Service{
		secret: []byte(cfg.Secret),
		issuer: cfg.Issuer,
		ttl:    ttl,
	}, nil
}

func (s *Service) SignToken(userID uint, role string) (string, error) {
	now := time.Now().UTC()
	claims := Claims{
		UserID: userID,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.issuer,
			Subject:   fmt.Sprintf("%d", userID),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.ttl)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.secret)
}

func (s *Service) ParseToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method %s", token.Method.Alg())
		}
		return s.secret, nil
	}, jwt.WithIssuer(s.issuer))
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token claims")
	}
	return claims, nil
}
