package config

import (
	"errors"
	"os"
	"strconv"

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

	FeishuAppID             string
	FeishuAppSecret         string
	FeishuVerificationToken string
	FeishuEncryptKey        string
	FeishuBaseURL           string
	FeishuEventMode         string
	FeishuOAuthRedirectURI  string
}

func Load() (Config, error) {
	_ = godotenv.Load()

	redisDB, err := strconv.Atoi(getenv("REDIS_DB", "0"))
	if err != nil {
		return Config{}, errors.New("REDIS_DB must be an integer")
	}

	return Config{
		AppEnv:                  getenv("APP_ENV", "local"),
		HTTPAddr:                getenv("HTTP_ADDR", ":8080"),
		RunMigrations:           getenv("RUN_MIGRATIONS", "true") == "true",
		MigrationsDir:           getenv("MIGRATIONS_DIR", "./migrations"),
		PostgresDSN:             getenv("POSTGRES_DSN", ""),
		RedisAddr:               getenv("REDIS_ADDR", "localhost:6379"),
		RedisPass:               getenv("REDIS_PASSWORD", ""),
		RedisDB:                 redisDB,
		FeishuAppID:             getenv("FEISHU_APP_ID", ""),
		FeishuAppSecret:         getenv("FEISHU_APP_SECRET", ""),
		FeishuVerificationToken: getenv("FEISHU_VERIFICATION_TOKEN", ""),
		FeishuEncryptKey:        getenv("FEISHU_ENCRYPT_KEY", ""),
		FeishuBaseURL:           getenv("FEISHU_BASE_URL", "https://open.feishu.cn"),
		FeishuEventMode:         getenv("FEISHU_EVENT_MODE", "websocket"),
		FeishuOAuthRedirectURI:  getenv("FEISHU_OAUTH_REDIRECT_URI", "http://localhost:8081/api/auth/feishu/callback"),
	}, nil
}

func getenv(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
