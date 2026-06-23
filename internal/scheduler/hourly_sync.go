package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"feishu-kb-assistant/internal/auth"
	"feishu-kb-assistant/internal/knowledge"
	"feishu-kb-assistant/internal/syncer"

	"github.com/jackc/pgx/v5"
)

type AuthRepository interface {
	Latest(ctx context.Context) (auth.Session, error)
}

type FeishuClient interface {
	TenantAccessToken(ctx context.Context) (string, error)
	SendTextMessage(ctx context.Context, accessToken string, receiveID string, text string) (string, error)
}

type HistorySyncer interface {
	SyncSelectedHistory(ctx context.Context, days int) (syncer.Result, error)
}

type KnowledgeIndexer interface {
	BuildIndex(ctx context.Context, days int) (knowledge.IndexResult, error)
}

type HourlySync struct {
	logger    *slog.Logger
	authRepo  AuthRepository
	feishu    FeishuClient
	syncer    HistorySyncer
	indexer   KnowledgeIndexer
	interval  time.Duration
	days      int
	timeout   time.Duration
	mu        sync.Mutex
	isRunning bool
}

func NewHourlySync(logger *slog.Logger, authRepo AuthRepository, feishu FeishuClient, historySyncer HistorySyncer, indexer KnowledgeIndexer, interval time.Duration, days int) *HourlySync {
	if logger == nil {
		logger = slog.Default()
	}
	if interval <= 0 {
		interval = time.Hour
	}
	if days <= 0 {
		days = 2
	}
	timeout := interval
	if timeout < 30*time.Minute {
		timeout = 30 * time.Minute
	}
	return &HourlySync{
		logger:   logger,
		authRepo: authRepo,
		feishu:   feishu,
		syncer:   historySyncer,
		indexer:  indexer,
		interval: interval,
		days:     days,
		timeout:  timeout,
	}
}

func (s *HourlySync) Start(ctx context.Context) {
	s.logger.Info("starting hourly history sync", "interval", s.interval.String(), "days", s.days)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("stopped hourly history sync")
			return
		case <-ticker.C:
			s.run(ctx)
		}
	}
}

func (s *HourlySync) run(parent context.Context) {
	if !s.tryLock() {
		s.logger.Warn("skip hourly history sync because previous run is still active")
		return
	}
	defer s.unlock()

	started := time.Now()
	ctx, cancel := context.WithTimeout(parent, s.timeout)
	defer cancel()

	session, err := s.authRepo.Latest(ctx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			s.logger.Warn("skip hourly history sync without feishu authorization")
			return
		}
		s.logger.Warn("load authorization for hourly sync", "error", err)
		return
	}
	s.logger.Info("running hourly history sync", "days", s.days, "open_id", session.OpenID)

	syncResult, syncErr := s.syncer.SyncSelectedHistory(ctx, s.days)
	var indexResult knowledge.IndexResult
	var indexErr error
	if syncErr == nil {
		indexResult, indexErr = s.indexer.BuildIndex(ctx, s.days)
	}

	status := "完成"
	if syncErr != nil || indexErr != nil {
		status = "失败"
	}
	duration := time.Since(started).Round(time.Second)
	notice := buildNotice(status, duration, syncResult, syncErr, indexResult, indexErr)
	if err := s.notify(ctx, session.OpenID, notice); err != nil {
		s.logger.Warn("send hourly sync notice", "open_id", session.OpenID, "error", err)
	}
	if syncErr != nil || indexErr != nil {
		s.logger.Warn("hourly history sync finished with error", "duration", duration.String(), "sync_error", syncErr, "index_error", indexErr)
		return
	}
	s.logger.Info("hourly history sync finished", "duration", duration.String(), "messages", syncResult.SavedMessages, "units", indexResult.Units, "chunks", indexResult.Chunks)
}

func (s *HourlySync) tryLock() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.isRunning {
		return false
	}
	s.isRunning = true
	return true
}

func (s *HourlySync) unlock() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.isRunning = false
}

func (s *HourlySync) notify(ctx context.Context, openID string, text string) error {
	if strings.TrimSpace(openID) == "" {
		return fmt.Errorf("authorized user open_id is empty")
	}
	token, err := s.feishu.TenantAccessToken(ctx)
	if err != nil {
		return err
	}
	_, err = s.feishu.SendTextMessage(ctx, token, openID, text)
	return err
}

func buildNotice(status string, duration time.Duration, syncResult syncer.Result, syncErr error, indexResult knowledge.IndexResult, indexErr error) string {
	var builder strings.Builder
	_, _ = fmt.Fprintf(&builder, "飞书知识库小时同步%s\n", status)
	_, _ = fmt.Fprintf(&builder, "耗时：%s\n", duration.String())
	if syncErr == nil {
		_, _ = fmt.Fprintf(&builder, "聊天同步：%d 个来源，%d 条消息\n", syncResult.SyncedSources, syncResult.SavedMessages)
	} else {
		_, _ = fmt.Fprintf(&builder, "聊天同步失败：%s\n", syncErr.Error())
	}
	if indexErr == nil && syncErr == nil {
		_, _ = fmt.Fprintf(&builder, "向量索引：%d 个知识单元，%d 个片段", indexResult.Units, indexResult.Chunks)
	} else if indexErr != nil {
		_, _ = fmt.Fprintf(&builder, "向量索引失败：%s", indexErr.Error())
	}
	return strings.TrimSpace(builder.String())
}
