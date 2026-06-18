package auth

import "time"

type Session struct {
	ID                    int64      `json:"id"`
	OpenID                string     `json:"open_id"`
	UnionID               string     `json:"union_id"`
	UserID                string     `json:"user_id"`
	Name                  string     `json:"name"`
	Email                 string     `json:"email"`
	TenantKey             string     `json:"tenant_key"`
	AccessToken           string     `json:"-"`
	RefreshToken          string     `json:"-"`
	AccessTokenExpiresAt  *time.Time `json:"access_token_expires_at"`
	RefreshTokenExpiresAt *time.Time `json:"refresh_token_expires_at"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}
