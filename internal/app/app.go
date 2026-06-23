package app

import (
	"context"
	"log/slog"
	"strings"

	openai "feishu-kb-assistant/internal/ai/openai"
	"feishu-kb-assistant/internal/auth"
	"feishu-kb-assistant/internal/autoreply"
	"feishu-kb-assistant/internal/config"
	"feishu-kb-assistant/internal/feishu"
	"feishu-kb-assistant/internal/httpapi"
	"feishu-kb-assistant/internal/infra/postgres"
	redisinfra "feishu-kb-assistant/internal/infra/redis"
	"feishu-kb-assistant/internal/knowledge"
	"feishu-kb-assistant/internal/media"
	"feishu-kb-assistant/internal/message"
	"feishu-kb-assistant/internal/scheduler"
	"feishu-kb-assistant/internal/source"
	"feishu-kb-assistant/internal/syncer"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type App struct {
	cfg    config.Config
	logger *slog.Logger
	db     *pgxpool.Pool
	redis  *redis.Client
	router *gin.Engine
	cancel context.CancelFunc
}

func New(ctx context.Context, cfg config.Config, logger *slog.Logger) (*App, error) {
	db, err := postgres.Connect(ctx, cfg.PostgresDSN)
	if err != nil {
		return nil, err
	}

	if cfg.RunMigrations {
		if err := postgres.RunMigrations(ctx, db, cfg.MigrationsDir); err != nil {
			db.Close()
			return nil, err
		}
	}

	redisClient := redisinfra.Connect(cfg.RedisAddr, cfg.RedisPass, cfg.RedisDB)
	if err := redisClient.Ping(ctx).Err(); err != nil {
		db.Close()
		return nil, err
	}

	feishuClient := feishu.NewClient(cfg.FeishuBaseURL, cfg.FeishuAppID, cfg.FeishuAppSecret, cfg.FeishuOAuthRedirectURI, redisClient)
	messageRepo := message.NewRepository(db)
	authRepo := auth.NewRepository(db)
	sourceRepo := source.NewRepository(db)
	openaiClient := openai.NewClientWithVision(
		cfg.OpenAIBaseURL,
		cfg.OpenAIAPIKey,
		cfg.OpenAIModel,
		cfg.OpenAIEmbeddingBaseURL,
		cfg.OpenAIEmbeddingAPIKey,
		cfg.OpenAIEmbeddingModel,
		cfg.OpenAIEmbeddingDims,
		cfg.VisionBaseURL,
		cfg.VisionAPIKey,
		cfg.VisionModel,
	)
	knowledgeService := knowledge.NewService(db, openaiClient, cfg.OpenAIEnableEmbeddings)
	mediaEnricher := media.NewEnricher(logger, cfg.VisionEnabled, feishuClient, openaiClient, cfg.VisionMaxImageBytes)
	eventHandler := feishu.NewEventHandler(cfg, logger, redisClient, messageRepo)
	eventHandler.SetMessageEnricher(mediaEnricher, feishuClient.TenantAccessToken)
	autoReplyService := autoreply.New(logger, authRepo, messageRepo, sourceRepo, feishuClient, knowledgeService)
	eventHandler.SetAutoReply(autoReplyService)
	router := httpapi.NewRouter(cfg, logger, db, redisClient, feishuClient, eventHandler, messageRepo, knowledgeService, mediaEnricher)

	runCtx, cancel := context.WithCancel(context.Background())
	if cfg.FeishuTokenRefreshEnabled {
		tokenRefresher := auth.NewTokenRefresher(
			logger,
			authRepo,
			feishuClient,
			cfg.FeishuTokenRefreshInterval,
			cfg.FeishuTokenRefreshBefore,
		)
		go tokenRefresher.Start(runCtx)
	}
	if cfg.FeishuP2PPollEnabled {
		p2pPoller := autoreply.NewP2PPoller(
			logger,
			authRepo,
			feishuClient,
			sourceRepo,
			messageRepo,
			autoReplyService,
			cfg.FeishuP2PPollInterval,
			cfg.FeishuP2PPollLookback,
		)
		p2pPoller.SetMessageEnricher(mediaEnricher)
		go p2pPoller.Start(runCtx)
	}
	if cfg.FeishuHourlySyncEnabled {
		historySyncer := syncer.NewRunner(db, feishuClient, sourceRepo, messageRepo, func(ctx context.Context) (string, error) {
			session, err := authRepo.Latest(ctx)
			if err != nil {
				return "", err
			}
			return session.AccessToken, nil
		})
		historySyncer.SetMessageEnricher(mediaEnricher)
		hourlySync := scheduler.NewHourlySync(
			logger,
			authRepo,
			feishuClient,
			historySyncer,
			knowledgeService,
			cfg.FeishuHourlySyncInterval,
			cfg.FeishuHourlySyncDays,
		)
		go hourlySync.Start(runCtx)
	}
	if shouldStartWebSocket(cfg.FeishuEventMode) {
		wsRunner := feishu.NewWebSocketRunner(cfg.FeishuAppID, cfg.FeishuAppSecret, logger, eventHandler)
		go func() {
			if err := wsRunner.Start(runCtx); err != nil {
				logger.Error("feishu websocket stopped", "error", err)
			}
		}()
	}

	return &App{
		cfg:    cfg,
		logger: logger,
		db:     db,
		redis:  redisClient,
		router: router,
		cancel: cancel,
	}, nil
}

func (a *App) Router() *gin.Engine {
	return a.router
}

func (a *App) Close() {
	if a.cancel != nil {
		a.cancel()
	}
	if a.redis != nil {
		_ = a.redis.Close()
	}
	if a.db != nil {
		a.db.Close()
	}
}

func shouldStartWebSocket(mode string) bool {
	switch strings.ToLower(mode) {
	case "websocket", "ws", "both":
		return true
	default:
		return false
	}
}
