package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"feishu-kb-assistant/internal/auth"
	"feishu-kb-assistant/internal/chat"
	"feishu-kb-assistant/internal/config"
	"feishu-kb-assistant/internal/feishu"
	"feishu-kb-assistant/internal/knowledge"
	"feishu-kb-assistant/internal/message"
	"feishu-kb-assistant/internal/source"
	"feishu-kb-assistant/internal/syncer"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func NewRouter(
	cfg config.Config,
	logger *slog.Logger,
	db *pgxpool.Pool,
	redisClient *redis.Client,
	feishuClient *feishu.Client,
	eventHandler *feishu.EventHandler,
	messageRepo *message.Repository,
	knowledgeService *knowledge.Service,
) *gin.Engine {
	if cfg.AppEnv == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(requestLogger(logger))

	chatRepo := chat.NewRepository(db)
	authRepo := auth.NewRepository(db)
	sourceRepo := source.NewRepository(db)
	historySyncer := syncer.NewRunner(db, feishuClient, sourceRepo, messageRepo, func(ctx context.Context) (string, error) {
		session, err := authRepo.Latest(ctx)
		if err != nil {
			return "", err
		}
		return session.AccessToken, nil
	})

	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	router.GET("/readyz", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()

		if err := db.Ping(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "error", "postgres": err.Error()})
			return
		}
		if err := redisClient.Ping(ctx).Err(); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "error", "redis": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	router.POST("/api/feishu/events", eventHandler.Handle)
	router.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusFound, "/admin")
	})
	router.GET("/admin", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(adminHTML))
	})

	router.GET("/api/auth/feishu/login", func(c *gin.Context) {
		state, err := randomState()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if err := redisClient.Set(c.Request.Context(), "oauth:state:"+state, "1", 10*time.Minute).Err(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Redirect(http.StatusFound, feishuClient.OAuthAuthorizeURL(state))
	})

	router.GET("/api/auth/feishu/callback", func(c *gin.Context) {
		code := c.Query("code")
		state := c.Query("state")
		if code == "" || state == "" {
			c.String(http.StatusBadRequest, "missing code or state")
			return
		}
		key := "oauth:state:" + state
		ok, err := redisClient.Del(c.Request.Context(), key).Result()
		if err != nil {
			c.String(http.StatusInternalServerError, "verify state failed: %s", err.Error())
			return
		}
		if ok == 0 {
			c.String(http.StatusBadRequest, "invalid or expired state")
			return
		}
		result, err := feishuClient.ExchangeOAuthCode(c.Request.Context(), code)
		if err != nil {
			c.String(http.StatusBadGateway, "exchange oauth code failed: %s", err.Error())
			return
		}
		if _, err := authRepo.SaveOAuthSession(c.Request.Context(), result); err != nil {
			c.String(http.StatusInternalServerError, "save oauth session failed: %s", err.Error())
			return
		}
		c.Redirect(http.StatusFound, "/admin")
	})

	admin := router.Group("/api/admin")
	admin.GET("/me", func(c *gin.Context) {
		session, err := authRepo.Latest(c.Request.Context())
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				c.JSON(http.StatusOK, gin.H{"authorized": false})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"authorized": true, "user": session})
	})

	admin.GET("/feishu/token", func(c *gin.Context) {
		token, err := feishuClient.TenantAccessToken(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "token_prefix": tokenPrefix(token)})
	})

	admin.GET("/messages/stats", func(c *gin.Context) {
		count, err := messageRepo.CountMessages(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message_count": count})
	})

	admin.POST("/index", func(c *gin.Context) {
		var req struct {
			Days int `json:"days"`
		}
		if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
		defer cancel()
		result, err := knowledgeService.BuildIndex(ctx, req.Days)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, result)
	})

	admin.POST("/ask", func(c *gin.Context) {
		var req struct {
			Question string `json:"question"`
			Limit    int    `json:"limit"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
		defer cancel()
		result, err := knowledgeService.Ask(ctx, req.Question, req.Limit)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, result)
	})

	admin.POST("/sync/history", func(c *gin.Context) {
		var req struct {
			Days int `json:"days"`
		}
		if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if req.Days <= 0 {
			req.Days = 30
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
		defer cancel()
		result, err := historySyncer.SyncSelectedHistory(ctx, req.Days)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, pgx.ErrNoRows) {
				status = http.StatusUnauthorized
			}
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, result)
	})

	admin.POST("/source/contacts/resolve-chats", func(c *gin.Context) {
		token, err := feishuClient.TenantAccessToken(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		contacts, err := sourceRepo.ListSelectedContacts(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		type itemResult struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			ChatID string `json:"chat_id,omitempty"`
			Error  string `json:"error,omitempty"`
		}
		results := make([]itemResult, 0, len(contacts))
		resolved := 0
		for _, contact := range contacts {
			result := itemResult{ID: firstNonEmpty(contact.OpenID, contact.UserID), Name: contact.Name, ChatID: contact.ChatID}
			if contact.ChatID != "" {
				resolved++
				results = append(results, result)
				continue
			}
			if contact.OpenID == "" {
				result.Error = "missing_open_id"
				results = append(results, result)
				continue
			}
			chatID, err := feishuClient.SendTextMessage(c.Request.Context(), token, contact.OpenID, "已将此单聊加入个人知识库同步范围。")
			if err != nil {
				result.Error = err.Error()
				results = append(results, result)
				continue
			}
			if err := sourceRepo.SaveContactChatID(c.Request.Context(), contact.OpenID, chatID); err != nil {
				result.Error = err.Error()
				results = append(results, result)
				continue
			}
			result.ChatID = chatID
			resolved++
			results = append(results, result)
		}
		c.JSON(http.StatusOK, gin.H{"resolved": resolved, "items": results})
	})

	admin.GET("/chats", func(c *gin.Context) {
		items, err := chatRepo.List(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": items})
	})

	admin.POST("/chats", func(c *gin.Context) {
		var req chat.UpsertRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		item, err := chatRepo.Upsert(c.Request.Context(), req)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, item)
	})

	admin.GET("/source/chats", func(c *gin.Context) {
		if c.Query("local") == "true" {
			items, err := sourceRepo.ListCachedChats(c.Request.Context())
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"items": items})
			return
		}
		session, err := authRepo.Latest(c.Request.Context())
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, pgx.ErrNoRows) {
				status = http.StatusUnauthorized
			}
			c.JSON(status, gin.H{"error": "feishu user authorization required"})
			return
		}
		items, err := feishuClient.ListUserChats(c.Request.Context(), session.AccessToken)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		if err := sourceRepo.CacheChats(c.Request.Context(), items); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		cached, err := sourceRepo.ListCachedChats(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": cached})
	})

	admin.POST("/source/chats/select", func(c *gin.Context) {
		var req struct {
			Items []source.RemoteChat `json:"items"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := sourceRepo.SaveSelectedChats(c.Request.Context(), req.Items); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "count": len(req.Items)})
	})

	admin.GET("/source/contacts", func(c *gin.Context) {
		if c.Query("local") == "true" {
			items, err := sourceRepo.ListCachedContacts(c.Request.Context())
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"items": items})
			return
		}
		departmentID := c.Query("department_id")
		query := strings.TrimSpace(c.Query("q"))
		session, err := authRepo.Latest(c.Request.Context())
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, pgx.ErrNoRows) {
				status = http.StatusUnauthorized
			}
			c.JSON(status, gin.H{"error": "feishu user authorization required"})
			return
		}
		var items []source.RemoteContact
		if query != "" {
			items, err = feishuClient.SearchUsers(c.Request.Context(), session.AccessToken, query)
		} else if departmentID != "" {
			items, err = feishuClient.ListDepartmentUsers(c.Request.Context(), session.AccessToken, departmentID)
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请输入姓名关键词，或填写 department_id 使用部门拉取"})
			return
		}
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		if err := sourceRepo.CacheContacts(c.Request.Context(), items); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		cached, err := sourceRepo.ListCachedContacts(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": cached})
	})

	admin.POST("/source/contacts/select", func(c *gin.Context) {
		var req struct {
			Items []source.RemoteContact `json:"items"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := sourceRepo.SaveSelectedContacts(c.Request.Context(), req.Items); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "count": len(req.Items)})
	})

	return router
}

func requestLogger(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		logger.Info(
			"http request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"latency", time.Since(start).String(),
		)
	}
}

func tokenPrefix(token string) string {
	if len(token) <= 8 {
		return token
	}
	return token[:8] + "..."
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func randomState() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

const adminHTML = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>Feishu KB Assistant</title>
  <style>
    :root {
      --ink: #202421;
      --graphite: #4b514d;
      --mist: #eef2ef;
      --paper: #fbfcfa;
      --line: #d9dfd8;
      --moss: #2f5d50;
      --moss-dark: #24493f;
      --amber: #b86b25;
      --cyan: #2f7d8c;
      --danger: #a34035;
      --white: #ffffff;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      font-family: Inter, ui-sans-serif, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      background: linear-gradient(180deg, #f7faf7 0, #eef2ef 100%);
      color: var(--ink);
      letter-spacing: 0;
    }
    button, a.button, input, select { font: inherit; }
    header {
      min-height: 74px;
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 18px;
      padding: 16px 28px;
      background: rgba(251, 252, 250, 0.94);
      border-bottom: 1px solid var(--line);
      position: sticky;
      top: 0;
      z-index: 4;
      backdrop-filter: blur(10px);
    }
    main {
      max-width: 1360px;
      margin: 0 auto;
      padding: 22px 22px 48px;
      display: grid;
      grid-template-columns: 310px minmax(0, 1fr);
      gap: 18px;
    }
    h1 {
      font-family: Georgia, "Times New Roman", serif;
      font-size: 26px;
      line-height: 1;
      margin: 0;
      font-weight: 700;
    }
    h2 {
      font-size: 15px;
      margin: 0;
      font-weight: 760;
    }
    .brand-kicker {
      margin-top: 6px;
      color: var(--graphite);
      font-size: 12px;
    }
    .shell-title { display: flex; align-items: center; gap: 12px; }
    .mark {
      width: 36px;
      height: 36px;
      border-radius: 8px;
      background: var(--moss);
      color: var(--white);
      display: grid;
      place-items: center;
      font-weight: 800;
      box-shadow: inset 0 -8px 0 rgba(0,0,0,.12);
    }
    .timeline {
      display: grid;
      grid-template-columns: 74px 1fr 46px 1fr 54px;
      align-items: center;
      gap: 8px;
      min-width: 420px;
      color: var(--graphite);
      font-size: 12px;
    }
    .timeline b { color: var(--ink); font-size: 11px; text-transform: uppercase; }
    .rail {
      height: 7px;
      border: 1px solid var(--line);
      background: repeating-linear-gradient(90deg, #dfe6df 0, #dfe6df 9px, transparent 9px, transparent 18px);
      border-radius: 999px;
      position: relative;
      overflow: hidden;
    }
    .rail:after {
      content: "";
      position: absolute;
      inset: 0;
      width: 46%;
      background: var(--amber);
    }
    .panel {
      background: rgba(255,255,255,.88);
      border: 1px solid var(--line);
      border-radius: 8px;
      box-shadow: 0 10px 32px rgba(32, 36, 33, .06);
    }
    .sidebar { display: flex; flex-direction: column; gap: 14px; }
    .side-card { padding: 16px; }
    .side-card h2 { margin-bottom: 12px; }
    .status-box {
      border: 1px solid var(--line);
      border-radius: 8px;
      padding: 12px;
      background: var(--paper);
      min-height: 78px;
      white-space: pre-wrap;
      font-size: 13px;
      line-height: 1.5;
    }
    .metric-grid { display: grid; grid-template-columns: repeat(2, 1fr); gap: 8px; }
    .metric {
      border: 1px solid var(--line);
      border-radius: 8px;
      padding: 10px;
      background: var(--paper);
    }
    .metric strong { display: block; font-size: 22px; }
    .metric span { color: var(--graphite); font-size: 12px; }
    .sync-controls { display: grid; gap: 8px; }
    .sync-result {
      margin-top: 10px;
      color: var(--graphite);
      font-size: 12px;
      line-height: 1.5;
      white-space: pre-wrap;
    }
    .content { min-width: 0; display: grid; gap: 16px; }
    .section-head {
      padding: 16px 18px 0;
      display: flex;
      justify-content: space-between;
      gap: 12px;
      align-items: flex-start;
    }
    .section-copy { color: var(--graphite); font-size: 13px; margin-top: 5px; }
    .toolbar {
      display: grid;
      grid-template-columns: minmax(220px, 1fr) auto auto auto;
      gap: 10px;
      padding: 14px 18px;
      align-items: center;
      border-bottom: 1px solid var(--line);
    }
    .contact-toolbar { grid-template-columns: minmax(190px, .8fr) minmax(220px, 1fr) auto auto auto; }
    .ask-toolbar { grid-template-columns: minmax(260px, 1fr) auto; }
    input:not([type="checkbox"]), select {
      border: 1px solid var(--line);
      background: var(--white);
      color: var(--ink);
      border-radius: 7px;
      padding: 9px 10px;
      min-height: 38px;
      outline: none;
      min-width: 0;
    }
    input:not([type="checkbox"]):focus, select:focus {
      border-color: var(--moss);
      box-shadow: 0 0 0 3px rgba(47, 93, 80, .12);
    }
    input[type="checkbox"] {
      width: 16px;
      height: 16px;
      min-width: 16px;
      min-height: 16px;
      margin: 0;
      accent-color: var(--moss);
      vertical-align: middle;
    }
    button, a.button {
      border: 1px solid var(--moss);
      background: var(--moss);
      color: var(--white);
      border-radius: 7px;
      padding: 9px 12px;
      cursor: pointer;
      text-decoration: none;
      font-size: 13px;
      min-height: 38px;
      white-space: nowrap;
    }
    button:hover, a.button:hover { background: var(--moss-dark); }
    button.secondary {
      background: var(--white);
      color: var(--ink);
      border-color: var(--line);
    }
    button.secondary:hover { background: var(--mist); }
    button:disabled { opacity: .52; cursor: not-allowed; }
    .table-wrap { overflow: auto; }
    table { width: 100%; border-collapse: collapse; font-size: 13px; table-layout: fixed; }
    th, td { border-bottom: 1px solid #edf1ec; padding: 11px 10px; text-align: left; vertical-align: middle; }
    th {
      color: var(--graphite);
      font-weight: 680;
      background: #f7faf7;
      position: sticky;
      top: 0;
      z-index: 1;
    }
    tr:hover td { background: #fbf7f0; }
    th:first-child, td:first-child { width: 44px; }
    .mono { font-family: "SFMono-Regular", Consolas, monospace; color: var(--graphite); font-size: 12px; overflow-wrap: anywhere; }
    .badge {
      display: inline-flex;
      align-items: center;
      min-height: 24px;
      padding: 3px 8px;
      border-radius: 999px;
      background: #ecf4f1;
      color: var(--moss-dark);
      border: 1px solid #cfe0d9;
      font-size: 12px;
    }
    .badge.enabled {
      background: #e8f3ee;
      color: var(--moss-dark);
      border-color: #bcd8ce;
    }
    .badge.idle {
      background: #f6f1e9;
      color: #715337;
      border-color: #e4d5bf;
    }
    .empty, .error, .loading {
      padding: 26px 18px;
      color: var(--graphite);
      font-size: 13px;
    }
    .error { color: var(--danger); }
    .pager {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 12px;
      padding: 12px 18px 16px;
      color: var(--graphite);
      font-size: 13px;
    }
    .pager-actions { display: flex; align-items: center; gap: 8px; }
    .pager button { min-height: 32px; padding: 6px 10px; }
    .toast {
      position: fixed;
      right: 20px;
      bottom: 20px;
      max-width: 360px;
      background: var(--ink);
      color: var(--white);
      border-radius: 8px;
      padding: 12px 14px;
      box-shadow: 0 14px 36px rgba(0,0,0,.22);
      opacity: 0;
      transform: translateY(12px);
      pointer-events: none;
      transition: opacity .18s ease, transform .18s ease;
      z-index: 8;
      font-size: 13px;
    }
    .toast.show { opacity: 1; transform: translateY(0); }
    .muted { color: var(--graphite); font-size: 13px; line-height: 1.5; }
    .answer-box {
      min-height: 160px;
      padding: 16px 18px;
      border-top: 1px solid var(--line);
      white-space: pre-wrap;
      color: var(--ink);
      line-height: 1.65;
      font-size: 13px;
      background: var(--paper);
    }
    @media (max-width: 980px) {
      header { align-items: flex-start; flex-direction: column; }
      main { grid-template-columns: 1fr; }
      .timeline { min-width: 0; width: 100%; grid-template-columns: 64px 1fr 40px 1fr 48px; }
      .toolbar, .contact-toolbar, .ask-toolbar { grid-template-columns: 1fr; }
    }
  </style>
</head>
<body>
  <header>
    <div class="shell-title">
      <div class="mark">KB</div>
      <div>
        <h1>知识源控制台</h1>
        <div class="brand-kicker">把飞书会话整理成可追溯的日切知识单元</div>
      </div>
    </div>
    <div class="timeline" aria-label="知识处理流程">
      <b>messages</b><div class="rail"></div><b>day cut</b><div class="rail"></div><b>chunks</b>
    </div>
  </header>
  <main>
    <aside class="sidebar">
      <section class="panel side-card">
        <h2>飞书授权</h2>
        <div id="me" class="status-box">加载中...</div>
        <div style="height: 12px"></div>
        <a class="button" href="/api/auth/feishu/login">连接飞书账号</a>
      </section>
      <section class="panel side-card">
        <h2>本次选择</h2>
        <div class="metric-grid">
          <div class="metric"><strong id="chatSelectedCount">0</strong><span>群组</span></div>
          <div class="metric"><strong id="contactSelectedCount">0</strong><span>联系人</span></div>
          <div class="metric"><strong id="chatLoadedCount">0</strong><span>已加载群</span></div>
          <div class="metric"><strong id="contactLoadedCount">0</strong><span>已加载人</span></div>
        </div>
      </section>
      <section class="panel side-card">
        <h2>知识单元策略</h2>
        <div class="muted">当前不把单条消息直接入库。选中的群会先按群和日期聚合，再切分成可检索片段。</div>
      </section>
      <section class="panel side-card">
        <h2>历史消息同步</h2>
        <div class="sync-controls">
          <select id="historyDays">
            <option value="7">近 7 天</option>
            <option value="30" selected>近 30 天</option>
            <option value="90">近 90 天</option>
            <option value="180">近 180 天</option>
          </select>
          <button id="syncHistoryButton" onclick="syncHistory()">同步历史消息</button>
        </div>
        <div id="syncResult" class="sync-result">会用当前授权用户身份同步。联系人单聊会自动尝试解析 Chat ID。</div>
      </section>
      <section class="panel side-card">
        <h2>知识索引</h2>
        <div class="sync-controls">
          <select id="indexDays">
            <option value="7">近 7 天</option>
            <option value="30" selected>近 30 天</option>
            <option value="90">近 90 天</option>
          </select>
          <button id="buildIndexButton" onclick="buildIndex()">构建索引</button>
        </div>
        <div id="indexResult" class="sync-result">把聊天记录聚合成知识单元并写入向量索引。</div>
      </section>
    </aside>
    <div class="content">
      <section class="panel">
        <div class="section-head">
          <div>
            <h2>知识库问答</h2>
            <div class="section-copy">先构建索引，再用 GPT 基于检索到的聊天上下文回答。</div>
          </div>
          <span class="badge" id="askBadge">待提问</span>
        </div>
        <div class="toolbar ask-toolbar">
          <input id="askQuestion" placeholder="输入问题，例如：上次部署问题怎么处理的？" />
          <button onclick="askKnowledge()">提问</button>
        </div>
        <div id="askAnswer" class="answer-box">答案会显示在这里。</div>
      </section>
      <section class="panel">
        <div class="section-head">
          <div>
            <h2>群组知识源</h2>
            <div class="section-copy">搜索群名或 Chat ID，勾选后写入本地知识源配置。</div>
          </div>
          <span class="badge" id="chatResultBadge">未加载</span>
        </div>
        <div class="toolbar">
          <input id="chatSearch" placeholder="搜索群名称 / Chat ID" oninput="setSearch('chat', this.value)" />
          <select id="chatPageSize" onchange="setPageSize('chat', this.value)">
            <option value="10">每页 10</option>
            <option value="20" selected>每页 20</option>
            <option value="50">每页 50</option>
          </select>
          <button onclick="loadChats()">拉取我的群组</button>
          <button class="secondary" onclick="saveChats()">保存选中群组</button>
        </div>
        <div class="table-wrap">
          <table>
            <thead><tr><th><input type="checkbox" id="chatCheckAll" onchange="togglePage('chat', this.checked)" /></th><th>群名称</th><th>Chat ID</th><th>知识库状态</th></tr></thead>
            <tbody id="chatRows"><tr><td colspan="4" class="empty">授权后拉取群组。</td></tr></tbody>
          </table>
        </div>
        <div class="pager">
          <div id="chatPageInfo">第 0 / 0 页</div>
          <div class="pager-actions">
            <button class="secondary" onclick="prevPage('chat')">上一页</button>
            <button class="secondary" onclick="nextPage('chat')">下一页</button>
          </div>
        </div>
      </section>
      <section class="panel">
        <div class="section-head">
          <div>
            <h2>联系人知识源</h2>
            <div class="section-copy">按姓名搜索联系人。同步时会用当前授权用户身份尝试读取单聊历史。</div>
          </div>
          <span class="badge" id="contactResultBadge">未加载</span>
        </div>
        <div class="toolbar contact-toolbar">
          <input id="contactRemoteQuery" placeholder="输入姓名搜索飞书用户" />
          <input id="contactSearch" placeholder="筛选结果：姓名 / Open ID / Email" oninput="setSearch('contact', this.value)" />
          <select id="contactPageSize" onchange="setPageSize('contact', this.value)">
            <option value="10">每页 10</option>
            <option value="20" selected>每页 20</option>
            <option value="50">每页 50</option>
          </select>
          <button onclick="loadContacts()">拉取联系人</button>
          <button class="secondary" onclick="saveContacts()">保存选中联系人</button>
        </div>
        <div class="table-wrap">
          <table>
            <thead><tr><th><input type="checkbox" id="contactCheckAll" onchange="togglePage('contact', this.checked)" /></th><th>姓名</th><th>Open ID</th><th>单聊 Chat ID</th><th>Email</th></tr></thead>
            <tbody id="contactRows"><tr><td colspan="5" class="empty">输入姓名后拉取联系人。</td></tr></tbody>
          </table>
        </div>
        <div class="pager">
          <div id="contactPageInfo">第 0 / 0 页</div>
          <div class="pager-actions">
            <button class="secondary" onclick="prevPage('contact')">上一页</button>
            <button class="secondary" onclick="nextPage('contact')">下一页</button>
          </div>
        </div>
      </div>
    </div>
  </main>
  <div id="toast" class="toast"></div>
  <script>
    const state = {
      chat: { items: [], selected: new Set(), query: '', page: 1, pageSize: 20 },
      contact: { items: [], selected: new Set(), query: '', page: 1, pageSize: 20 }
    };

    async function api(path, options) {
      const res = await fetch(path, options);
      const text = await res.text();
      let body;
      try { body = JSON.parse(text); } catch { body = { error: text }; }
      if (!res.ok) throw new Error(body.error || text || res.statusText);
      return body;
    }

    async function loadMe() {
      try {
        const data = await api('/api/admin/me');
        document.getElementById('me').innerHTML = data.authorized
          ? '<strong>已连接</strong><br>' + escapeHtml(data.user.name || data.user.open_id) + '<br><span class="mono">' + escapeHtml(data.user.email || data.user.open_id || '') + '</span>'
          : '<strong>未连接</strong><br>连接飞书账号后可拉取群组和联系人。';
      } catch (err) {
        document.getElementById('me').innerHTML = '<span class="error">' + escapeHtml(err.message) + '</span>';
      }
    }

    async function loadChats() {
      setRows('chatRows', '<tr><td colspan="4" class="loading">正在拉取群组...</td></tr>');
      try {
        const data = await api('/api/admin/source/chats');
        state.chat.items = data.items || [];
        restoreSelected('chat');
        state.chat.page = 1;
        render('chat');
        toast('已加载 ' + state.chat.items.length + ' 个群组');
      } catch (err) {
        setRows('chatRows', '<tr><td colspan="4" class="error">' + escapeHtml(err.message) + '</td></tr>');
      }
      updateMetrics();
    }

    async function saveChats() {
      const items = selectedItems('chat');
      if (!items.length) return toast('请选择群组');
      const result = await api('/api/admin/source/chats/select', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ items })
      });
      await loadCachedChats(false);
      toast('已保存 ' + result.count + ' 个群组');
    }

    async function loadCachedChats(showMessage) {
      try {
        const data = await api('/api/admin/source/chats?local=true');
        state.chat.items = data.items || [];
        restoreSelected('chat');
        render('chat');
        if (showMessage && state.chat.items.length) toast('已恢复 ' + state.chat.items.length + ' 个本地群组');
      } catch (err) {
        setRows('chatRows', '<tr><td colspan="4" class="error">' + escapeHtml(err.message) + '</td></tr>');
      }
    }

    async function loadContacts() {
      const query = document.getElementById('contactRemoteQuery').value.trim();
      if (!query) return toast('请输入姓名关键词');
      setRows('contactRows', '<tr><td colspan="4" class="loading">正在拉取联系人...</td></tr>');
      try {
        const data = await api('/api/admin/source/contacts?q=' + encodeURIComponent(query));
        state.contact.items = data.items || [];
        restoreSelected('contact');
        state.contact.page = 1;
        render('contact');
        toast('已加载 ' + state.contact.items.length + ' 个联系人');
      } catch (err) {
        setRows('contactRows', '<tr><td colspan="5" class="error">' + escapeHtml(err.message) + '</td></tr>');
      }
      updateMetrics();
    }

    async function saveContacts() {
      const items = selectedItems('contact');
      if (!items.length) return toast('请选择联系人');
      const result = await api('/api/admin/source/contacts/select', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ items })
      });
      await loadCachedContacts(false);
      toast('已保存 ' + result.count + ' 个联系人');
    }

    async function loadCachedContacts(showMessage) {
      try {
        const data = await api('/api/admin/source/contacts?local=true');
        state.contact.items = data.items || [];
        restoreSelected('contact');
        render('contact');
        if (showMessage && state.contact.items.length) toast('已恢复 ' + state.contact.items.length + ' 个本地联系人');
      } catch (err) {
        setRows('contactRows', '<tr><td colspan="5" class="error">' + escapeHtml(err.message) + '</td></tr>');
      }
    }

    async function syncHistory() {
      const button = document.getElementById('syncHistoryButton');
      const resultBox = document.getElementById('syncResult');
      const days = Number(document.getElementById('historyDays').value || 30);
      button.disabled = true;
      resultBox.textContent = '正在同步近 ' + days + ' 天历史消息...';
      try {
        const data = await api('/api/admin/sync/history', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ days })
        });
        const skipped = data.skipped_contacts || [];
        const lines = [
          '已同步来源 ' + data.synced_sources + ' 个',
          '写入消息 ' + data.saved_messages + ' 条',
          skipped.length ? '跳过联系人 ' + skipped.length + ' 个：用户态未找到单聊 Chat ID' : '联系人无跳过'
        ];
        resultBox.textContent = lines.join('\n');
        toast('历史消息同步完成：' + data.saved_messages + ' 条');
        updateMessageStats();
      } catch (err) {
        resultBox.textContent = err.message;
        toast('同步失败：' + err.message);
      } finally {
        button.disabled = false;
      }
    }

    async function buildIndex() {
      const button = document.getElementById('buildIndexButton');
      const resultBox = document.getElementById('indexResult');
      const days = Number(document.getElementById('indexDays').value || 30);
      button.disabled = true;
      resultBox.textContent = '正在构建近 ' + days + ' 天知识索引...';
      try {
        const data = await api('/api/admin/index', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ days })
        });
        resultBox.textContent = '已生成知识单元 ' + data.units + ' 个，向量片段 ' + data.chunks + ' 个。';
        toast('知识索引构建完成');
      } catch (err) {
        resultBox.textContent = err.message;
        toast('索引失败：' + err.message);
      } finally {
        button.disabled = false;
      }
    }

    async function askKnowledge() {
      const question = document.getElementById('askQuestion').value.trim();
      if (!question) return toast('请输入问题');
      const answerBox = document.getElementById('askAnswer');
      document.getElementById('askBadge').textContent = '思考中';
      answerBox.textContent = '正在检索聊天记录并生成答案...';
      try {
        const data = await api('/api/admin/ask', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ question, limit: 8 })
        });
        const sources = (data.sources || []).map((item, index) => '[' + (index + 1) + '] ' + item.source_id + ' · score ' + Number(item.score || 0).toFixed(4)).join('\n');
        answerBox.textContent = data.answer + (sources ? '\n\n参考片段：\n' + sources : '');
        document.getElementById('askBadge').textContent = '已回答';
      } catch (err) {
        answerBox.textContent = err.message;
        document.getElementById('askBadge').textContent = '失败';
        toast('问答失败：' + err.message);
      }
    }

    async function resolveContactChats() {
      const resultBox = document.getElementById('syncResult');
      resultBox.textContent = '正在解析选中联系人的单聊会话...';
      try {
        const data = await api('/api/admin/source/contacts/resolve-chats', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' }
        });
        resultBox.textContent = '已解析 ' + data.resolved + ' 个联系人单聊会话。';
        await loadCachedContacts(false);
        toast('联系人单聊已解析');
      } catch (err) {
        resultBox.textContent = err.message;
        toast('解析失败：' + err.message);
      }
    }

    async function updateMessageStats() {
      try {
        const data = await api('/api/admin/messages/stats');
        document.getElementById('syncResult').textContent += '\n当前消息库共 ' + data.message_count + ' 条';
      } catch {}
    }

    function render(type) {
      const rowsId = type + 'Rows';
      const data = pageItems(type);
      const colspan = type === 'chat' ? 4 : 5;
      if (!filteredItems(type).length) {
        setRows(rowsId, '<tr><td colspan="' + colspan + '" class="empty">没有匹配结果。</td></tr>');
      } else {
        setRows(rowsId, data.map(item => type === 'chat' ? chatRow(item) : contactRow(item)).join(''));
      }
      syncPageCheck(type);
      updatePager(type);
      updateMetrics();
    }

    function chatRow(item) {
      const key = item.chat_id || '';
      const enabled = state.chat.selected.has(key) || item.selected;
      const status = enabled ? '已启用' : '未启用';
      const badgeClass = enabled ? 'badge enabled' : 'badge idle';
      return '<tr><td><input type="checkbox" class="chat" data-key="' + escapeHtml(key) + '" onchange="toggleOne(\'chat\', this.dataset.key, this.checked)" ' + checkedAttr('chat', key) + ' /></td>' +
        '<td>' + escapeHtml(item.name || '') + '</td>' +
        '<td class="mono">' + escapeHtml(key) + '</td>' +
        '<td><span class="' + badgeClass + '">' + status + '</span></td></tr>';
    }

    function contactRow(item) {
      const key = item.open_id || item.user_id || '';
      return '<tr><td><input type="checkbox" class="contact" data-key="' + escapeHtml(key) + '" onchange="toggleOne(\'contact\', this.dataset.key, this.checked)" ' + checkedAttr('contact', key) + ' /></td>' +
        '<td>' + escapeHtml(item.name || '') + '</td>' +
        '<td class="mono">' + escapeHtml(key) + '</td>' +
        '<td class="mono">' + escapeHtml(item.chat_id || '-') + '</td>' +
        '<td>' + escapeHtml(item.email || '') + '</td></tr>';
    }

    function filteredItems(type) {
      const query = state[type].query.trim().toLowerCase();
      if (!query) return state[type].items;
      return state[type].items.filter(item => JSON.stringify(item).toLowerCase().includes(query));
    }

    function pageItems(type) {
      const model = state[type];
      const items = filteredItems(type);
      const totalPages = Math.max(1, Math.ceil(items.length / model.pageSize));
      model.page = Math.min(Math.max(1, model.page), totalPages);
      const start = (model.page - 1) * model.pageSize;
      return items.slice(start, start + model.pageSize);
    }

    function setSearch(type, value) {
      state[type].query = value;
      state[type].page = 1;
      render(type);
    }

    function setPageSize(type, value) {
      state[type].pageSize = Number(value);
      state[type].page = 1;
      render(type);
    }

    function prevPage(type) {
      state[type].page = Math.max(1, state[type].page - 1);
      render(type);
    }

    function nextPage(type) {
      const totalPages = Math.max(1, Math.ceil(filteredItems(type).length / state[type].pageSize));
      state[type].page = Math.min(totalPages, state[type].page + 1);
      render(type);
    }

    function updatePager(type) {
      const total = filteredItems(type).length;
      const totalPages = Math.max(1, Math.ceil(total / state[type].pageSize));
      document.getElementById(type + 'PageInfo').textContent = '第 ' + state[type].page + ' / ' + totalPages + ' 页 · 共 ' + total + ' 条';
      document.getElementById(type + 'ResultBadge').textContent = total ? '显示 ' + total + ' 条' : '无结果';
    }

    function toggleOne(type, key, checked) {
      if (!key) return;
      if (checked) state[type].selected.add(key); else state[type].selected.delete(key);
      syncPageCheck(type);
      updateMetrics();
    }

    function togglePage(type, checked) {
      pageItems(type).forEach(item => {
        const key = type === 'chat' ? item.chat_id : (item.open_id || item.user_id);
        if (!key) return;
        if (checked) state[type].selected.add(key); else state[type].selected.delete(key);
      });
      render(type);
    }

    function syncPageCheck(type) {
      const checkbox = document.getElementById(type + 'CheckAll');
      const page = pageItems(type);
      checkbox.checked = page.length > 0 && page.every(item => state[type].selected.has(type === 'chat' ? item.chat_id : (item.open_id || item.user_id)));
    }

    function selectedItems(type) {
      return state[type].items.filter(item => state[type].selected.has(type === 'chat' ? item.chat_id : (item.open_id || item.user_id)));
    }

    function restoreSelected(type) {
      state[type].selected.clear();
      state[type].items.forEach(item => {
        const key = type === 'chat' ? item.chat_id : (item.open_id || item.user_id);
        if (item.selected && key) state[type].selected.add(key);
      });
    }

    function checkedAttr(type, key) {
      return state[type].selected.has(key) ? 'checked' : '';
    }

    function setRows(id, html) { document.getElementById(id).innerHTML = html; }

    function updateMetrics() {
      document.getElementById('chatSelectedCount').textContent = state.chat.selected.size;
      document.getElementById('contactSelectedCount').textContent = state.contact.selected.size;
      document.getElementById('chatLoadedCount').textContent = state.chat.items.length;
      document.getElementById('contactLoadedCount').textContent = state.contact.items.length;
    }

    function toast(message) {
      const el = document.getElementById('toast');
      el.textContent = message;
      el.classList.add('show');
      clearTimeout(window.toastTimer);
      window.toastTimer = setTimeout(() => el.classList.remove('show'), 2400);
    }

    function escapeHtml(value) {
      return String(value).replace(/[&<>"']/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#039;' }[char]));
    }

    loadMe();
    loadCachedChats(true);
    loadCachedContacts(true);
    updateMetrics();
  </script>
</body>
</html>`
