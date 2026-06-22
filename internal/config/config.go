package config

import (
	"errors"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	AppEnv        string
	HTTPAddr      string
	RunMigrations bool
	MigrationsDir string

	PostgresDSN string
	RedisAddr   string
	RedisPass   string
	RedisDB     int

	FeishuAppID                string
	FeishuAppSecret            string
	FeishuVerificationToken    string
	FeishuEncryptKey           string
	FeishuBaseURL              string
	FeishuEventMode            string
	FeishuOAuthRedirectURI     string
	FeishuP2PPollEnabled       bool
	FeishuP2PPollInterval      time.Duration
	FeishuP2PPollLookback      time.Duration
	FeishuTokenRefreshEnabled  bool
	FeishuTokenRefreshInterval time.Duration
	FeishuTokenRefreshBefore   time.Duration

	OpenAIBaseURL          string
	OpenAIAPIKey           string
	OpenAIModel            string
	OpenAIEmbeddingModel   string
	OpenAIEmbeddingBaseURL string
	OpenAIEmbeddingAPIKey  string
	OpenAIEmbeddingDims    int
	OpenAIEnableEmbeddings bool
	OpenAIWireAPI          string
}

func Load() (Config, error) {
	_ = godotenv.Load()

	redisDB, err := strconv.Atoi(getenv("REDIS_DB", "0"))
	if err != nil {
		return Config{}, errors.New("REDIS_DB must be an integer")
	}
	embeddingDims, err := strconv.Atoi(getenv("OPENAI_EMBEDDING_DIMENSIONS", "1536"))
	if err != nil {
		return Config{}, errors.New("OPENAI_EMBEDDING_DIMENSIONS must be an integer")
	}
	p2pPollInterval, err := parseDurationEnv("FEISHU_P2P_POLL_INTERVAL", "60s")
	if err != nil {
		return Config{}, err
	}
	p2pPollLookback, err := parseDurationEnv("FEISHU_P2P_POLL_LOOKBACK", "2m")
	if err != nil {
		return Config{}, err
	}
	tokenRefreshInterval, err := parseDurationEnv("FEISHU_TOKEN_REFRESH_INTERVAL", "10m")
	if err != nil {
		return Config{}, err
	}
	tokenRefreshBefore, err := parseDurationEnv("FEISHU_TOKEN_REFRESH_BEFORE", "30m")
	if err != nil {
		return Config{}, err
	}

	return Config{
		AppEnv:                     getenv("APP_ENV", "local"),
		HTTPAddr:                   getenv("HTTP_ADDR", ":8080"),
		RunMigrations:              getenv("RUN_MIGRATIONS", "true") == "true",
		MigrationsDir:              getenv("MIGRATIONS_DIR", "./migrations"),
		PostgresDSN:                getenv("POSTGRES_DSN", ""),
		RedisAddr:                  getenv("REDIS_ADDR", "localhost:6379"),
		RedisPass:                  getenv("REDIS_PASSWORD", ""),
		RedisDB:                    redisDB,
		FeishuAppID:                getenv("FEISHU_APP_ID", ""),
		FeishuAppSecret:            getenv("FEISHU_APP_SECRET", ""),
		FeishuVerificationToken:    getenv("FEISHU_VERIFICATION_TOKEN", ""),
		FeishuEncryptKey:           getenv("FEISHU_ENCRYPT_KEY", ""),
		FeishuBaseURL:              getenv("FEISHU_BASE_URL", "https://open.feishu.cn"),
		FeishuEventMode:            getenv("FEISHU_EVENT_MODE", "websocket"),
		FeishuOAuthRedirectURI:     getenv("FEISHU_OAUTH_REDIRECT_URI", "http://localhost:8081/api/auth/feishu/callback"),
		FeishuP2PPollEnabled:       getenv("FEISHU_P2P_POLL_ENABLED", "true") == "true",
		FeishuP2PPollInterval:      p2pPollInterval,
		FeishuP2PPollLookback:      p2pPollLookback,
		FeishuTokenRefreshEnabled:  getenv("FEISHU_TOKEN_REFRESH_ENABLED", "true") == "true",
		FeishuTokenRefreshInterval: tokenRefreshInterval,
		FeishuTokenRefreshBefore:   tokenRefreshBefore,
		OpenAIBaseURL:              getenv("OPENAI_BASE_URL", getenv("base_url", "https://api.openai.com/v1")),
		OpenAIAPIKey:               getenv("OPENAI_API_KEY", ""),
		OpenAIModel:                getenv("OPENAI_MODEL", getenv("model", "gpt-5.5")),
		OpenAIEmbeddingModel:       getenv("OPENAI_EMBEDDING_MODEL", getenv("DASHSCOPE_EMBEDDING_MODEL", "text-embedding-3-small")),
		OpenAIEmbeddingBaseURL:     getenv("OPENAI_EMBEDDING_BASE_URL", getenv("DASHSCOPE_BASE_URL", "")),
		OpenAIEmbeddingAPIKey:      getenv("OPENAI_EMBEDDING_API_KEY", getenv("DASHSCOPE_API_KEY", "")),
		OpenAIEmbeddingDims:        embeddingDims,
		OpenAIEnableEmbeddings:     getenv("OPENAI_ENABLE_EMBEDDINGS", "false") == "true",
		OpenAIWireAPI:              getenv("OPENAI_WIRE_API", getenv("wire_api", "responses")),
	}, nil
}

func parseDurationEnv(key string, fallback string) (time.Duration, error) {
	duration, err := time.ParseDuration(getenv(key, fallback))
	if err != nil {
		return 0, errors.New(key + " must be a duration like 60s or 2m")
	}
	return duration, nil
}

func getenv(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
