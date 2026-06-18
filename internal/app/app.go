package app

import (
	"context"
	"log/slog"
	"strings"

	openai "feishu-kb-assistant/internal/ai/openai"
	"feishu-kb-assistant/internal/config"
	"feishu-kb-assistant/internal/feishu"
	"feishu-kb-assistant/internal/httpapi"
	"feishu-kb-assistant/internal/infra/postgres"
	redisinfra "feishu-kb-assistant/internal/infra/redis"
	"feishu-kb-assistant/internal/knowledge"
	"feishu-kb-assistant/internal/message"

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
	openaiClient := openai.NewClient(
		cfg.OpenAIBaseURL,
		cfg.OpenAIAPIKey,
		cfg.OpenAIModel,
		cfg.OpenAIEmbeddingBaseURL,
		cfg.OpenAIEmbeddingAPIKey,
		cfg.OpenAIEmbeddingModel,
		cfg.OpenAIEmbeddingDims,
	)
	knowledgeService := knowledge.NewService(db, openaiClient, cfg.OpenAIEnableEmbeddings)
	eventHandler := feishu.NewEventHandler(cfg, logger, redisClient, messageRepo)
	router := httpapi.NewRouter(cfg, logger, db, redisClient, feishuClient, eventHandler, messageRepo, knowledgeService)

	runCtx, cancel := context.WithCancel(context.Background())
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
