package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"feishu-kb-assistant/internal/feishu"

	"github.com/jackc/pgx/v5"
)

type RefreshFeishuClient interface {
	RefreshOAuthToken(ctx context.Context, refreshToken string) (feishu.OAuthTokenResult, error)
}

type RefreshRepository interface {
	Latest(ctx context.Context) (Session, error)
	UpdateSessionTokens(ctx context.Context, sessionID int64, result feishu.OAuthTokenResult) (Session, error)
}

type TokenRefresher struct {
	logger        *slog.Logger
	repo          RefreshRepository
	feishu        RefreshFeishuClient
	interval      time.Duration
	refreshBefore time.Duration
}

func NewTokenRefresher(logger *slog.Logger, repo RefreshRepository, feishuClient RefreshFeishuClient, interval time.Duration, refreshBefore time.Duration) *TokenRefresher {
	if logger == nil {
		logger = slog.Default()
	}
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	if refreshBefore <= 0 {
		refreshBefore = 30 * time.Minute
	}
	return &TokenRefresher{
		logger:        logger,
		repo:          repo,
		feishu:        feishuClient,
		interval:      interval,
		refreshBefore: refreshBefore,
	}
}

func (r *TokenRefresher) Start(ctx context.Context) {
	r.logger.Info("starting feishu oauth token refresher", "interval", r.interval.String(), "refresh_before", r.refreshBefore.String())
	r.refreshIfNeeded(ctx)

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("stopped feishu oauth token refresher")
			return
		case <-ticker.C:
			r.refreshIfNeeded(ctx)
		}
	}
}

func (r *TokenRefresher) refreshIfNeeded(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, minDuration(30*time.Second, r.interval))
	defer cancel()

	session, err := r.repo.Latest(ctx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			r.logger.Info("skip oauth token refresh without authorization")
			return
		}
		r.logger.Warn("load oauth session for refresh", "error", err)
		return
	}
	if strings.TrimSpace(session.RefreshToken) == "" {
		r.logger.Warn("skip oauth token refresh without refresh token", "session_id", session.ID, "open_id", session.OpenID)
		return
	}
	if !r.shouldRefresh(session) {
		return
	}

	result, err := r.feishu.RefreshOAuthToken(ctx, session.RefreshToken)
	if err != nil {
		r.logger.Warn("refresh oauth token", "session_id", session.ID, "open_id", session.OpenID, "error", err)
		return
	}
	if strings.TrimSpace(result.AccessToken) == "" {
		r.logger.Warn("refresh oauth token returned empty access token", "session_id", session.ID, "open_id", session.OpenID)
		return
	}
	updated, err := r.repo.UpdateSessionTokens(ctx, session.ID, result)
	if err != nil {
		r.logger.Warn("save refreshed oauth token", "session_id", session.ID, "open_id", session.OpenID, "error", err)
		return
	}
	r.logger.Info(
		"refreshed oauth token",
		"session_id", updated.ID,
		"open_id", updated.OpenID,
		"access_token_expires_at", formatTimePtr(updated.AccessTokenExpiresAt),
		"refresh_token_expires_at", formatTimePtr(updated.RefreshTokenExpiresAt),
	)
}

func (r *TokenRefresher) shouldRefresh(session Session) bool {
	now := time.Now()
	if session.RefreshTokenExpiresAt != nil && now.After(*session.RefreshTokenExpiresAt) {
		r.logger.Warn("oauth refresh token expired", "session_id", session.ID, "open_id", session.OpenID, "expired_at", session.RefreshTokenExpiresAt.Format(time.RFC3339))
		return false
	}
	if session.AccessTokenExpiresAt == nil {
		return false
	}
	return now.Add(r.refreshBefore).After(*session.AccessTokenExpiresAt)
}

func minDuration(a time.Duration, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func formatTimePtr(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.Format(time.RFC3339)
}

func (r *TokenRefresher) String() string {
	return fmt.Sprintf("TokenRefresher(interval=%s, refreshBefore=%s)", r.interval, r.refreshBefore)
}
