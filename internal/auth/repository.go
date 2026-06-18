package auth

import (
	"context"
	"fmt"
	"time"

	"feishu-kb-assistant/internal/feishu"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

func (r *Repository) SaveOAuthSession(ctx context.Context, result feishu.OAuthTokenResult) (Session, error) {
	var accessExpiresAt *time.Time
	if result.ExpiresIn > 0 {
		value := time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)
		accessExpiresAt = &value
	}
	var refreshExpiresAt *time.Time
	if result.RefreshExpiresIn > 0 {
		value := time.Now().Add(time.Duration(result.RefreshExpiresIn) * time.Second)
		refreshExpiresAt = &value
	}

	var session Session
	err := r.db.QueryRow(ctx, `
		INSERT INTO feishu_auth_sessions (
			open_id, union_id, user_id, name, email, tenant_key, access_token, refresh_token,
			access_token_expires_at, refresh_token_expires_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id, open_id, union_id, user_id, name, email, tenant_key, access_token, refresh_token,
		          access_token_expires_at, refresh_token_expires_at, created_at, updated_at
	`, result.OpenID, result.UnionID, result.UserID, result.Name, result.Email, result.TenantKey, result.AccessToken, result.RefreshToken, accessExpiresAt, refreshExpiresAt).Scan(
		&session.ID,
		&session.OpenID,
		&session.UnionID,
		&session.UserID,
		&session.Name,
		&session.Email,
		&session.TenantKey,
		&session.AccessToken,
		&session.RefreshToken,
		&session.AccessTokenExpiresAt,
		&session.RefreshTokenExpiresAt,
		&session.CreatedAt,
		&session.UpdatedAt,
	)
	if err != nil {
		return Session{}, fmt.Errorf("save oauth session: %w", err)
	}
	return session, nil
}

func (r *Repository) Latest(ctx context.Context) (Session, error) {
	var session Session
	err := r.db.QueryRow(ctx, `
		SELECT id, open_id, union_id, user_id, name, email, tenant_key, access_token, refresh_token,
		       access_token_expires_at, refresh_token_expires_at, created_at, updated_at
		FROM feishu_auth_sessions
		ORDER BY created_at DESC
		LIMIT 1
	`).Scan(
		&session.ID,
		&session.OpenID,
		&session.UnionID,
		&session.UserID,
		&session.Name,
		&session.Email,
		&session.TenantKey,
		&session.AccessToken,
		&session.RefreshToken,
		&session.AccessTokenExpiresAt,
		&session.RefreshTokenExpiresAt,
		&session.CreatedAt,
		&session.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return Session{}, err
		}
		return Session{}, fmt.Errorf("get latest oauth session: %w", err)
	}
	return session, nil
}
