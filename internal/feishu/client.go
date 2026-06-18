package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"feishu-kb-assistant/internal/source"

	"github.com/redis/go-redis/v9"
)

const tenantTokenCacheKey = "feishu:tenant_access_token"
const appTokenCacheKey = "feishu:app_access_token"

type Client struct {
	baseURL     string
	appID       string
	appSecret   string
	redirectURI string
	http        *http.Client
	redis       *redis.Client
}

func NewClient(baseURL string, appID string, appSecret string, redirectURI string, redisClient *redis.Client) *Client {
	return &Client{
		baseURL:     baseURL,
		appID:       appID,
		appSecret:   appSecret,
		redirectURI: redirectURI,
		http: &http.Client{
			Timeout: 10 * time.Second,
		},
		redis: redisClient,
	}
}

func (c *Client) OAuthAuthorizeURL(state string) string {
	values := url.Values{}
	values.Set("client_id", c.appID)
	values.Set("redirect_uri", c.redirectURI)
	values.Set("response_type", "code")
	values.Set("state", state)
	values.Set("scope", strings.Join([]string{
		"im:message",
		"im:message:readonly",
		"im:message.group_msg:get_as_user",
		"im:message.p2p_msg:get_as_user",
		"im:message.send_as_user",
	}, " "))
	return c.accountsBaseURL() + "/open-apis/authen/v1/authorize?" + values.Encode()
}

func (c *Client) accountsBaseURL() string {
	parsed, err := url.Parse(c.baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "https://accounts.feishu.cn"
	}
	parsed.Host = strings.Replace(parsed.Host, "open.", "accounts.", 1)
	return strings.TrimRight(parsed.String(), "/")
}

func (c *Client) TenantAccessToken(ctx context.Context) (string, error) {
	if c.appID == "" || c.appSecret == "" {
		return "", fmt.Errorf("FEISHU_APP_ID and FEISHU_APP_SECRET are required")
	}

	if token, err := c.redis.Get(ctx, tenantTokenCacheKey).Result(); err == nil && token != "" {
		return token, nil
	}

	body, err := json.Marshal(map[string]string{
		"app_id":     c.appID,
		"app_secret": c.appSecret,
	})
	if err != nil {
		return "", fmt.Errorf("marshal token request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/open-apis/auth/v3/tenant_access_token/internal", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("request tenant token: %w", err)
	}
	defer resp.Body.Close()

	var payload struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if resp.StatusCode >= 400 || payload.Code != 0 {
		return "", fmt.Errorf("tenant token failed: status=%d code=%d msg=%s", resp.StatusCode, payload.Code, payload.Msg)
	}

	ttl := time.Duration(payload.Expire-300) * time.Second
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	if err := c.redis.Set(ctx, tenantTokenCacheKey, payload.TenantAccessToken, ttl).Err(); err != nil {
		return "", fmt.Errorf("cache tenant token: %w", err)
	}

	return payload.TenantAccessToken, nil
}

func (c *Client) AppAccessToken(ctx context.Context) (string, error) {
	if c.appID == "" || c.appSecret == "" {
		return "", fmt.Errorf("FEISHU_APP_ID and FEISHU_APP_SECRET are required")
	}

	if token, err := c.redis.Get(ctx, appTokenCacheKey).Result(); err == nil && token != "" {
		return token, nil
	}

	body, err := json.Marshal(map[string]string{
		"app_id":     c.appID,
		"app_secret": c.appSecret,
	})
	if err != nil {
		return "", fmt.Errorf("marshal app token request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/open-apis/auth/v3/app_access_token/internal", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create app token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("request app token: %w", err)
	}
	defer resp.Body.Close()

	var payload struct {
		Code           int    `json:"code"`
		Msg            string `json:"msg"`
		AppAccessToken string `json:"app_access_token"`
		Expire         int    `json:"expire"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode app token response: %w", err)
	}
	if resp.StatusCode >= 400 || payload.Code != 0 {
		return "", fmt.Errorf("app token failed: status=%d code=%d msg=%s", resp.StatusCode, payload.Code, payload.Msg)
	}

	ttl := time.Duration(payload.Expire-300) * time.Second
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	if err := c.redis.Set(ctx, appTokenCacheKey, payload.AppAccessToken, ttl).Err(); err != nil {
		return "", fmt.Errorf("cache app token: %w", err)
	}

	return payload.AppAccessToken, nil
}

type OAuthTokenResult struct {
	AccessToken      string
	RefreshToken     string
	ExpiresIn        int
	RefreshExpiresIn int
	Name             string
	EnName           string
	AvatarURL        string
	OpenID           string
	UnionID          string
	UserID           string
	Email            string
	TenantKey        string
}

func (c *Client) ExchangeOAuthCode(ctx context.Context, code string) (OAuthTokenResult, error) {
	body, err := json.Marshal(map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     c.appID,
		"client_secret": c.appSecret,
		"code":          code,
		"redirect_uri":  c.redirectURI,
	})
	if err != nil {
		return OAuthTokenResult{}, fmt.Errorf("marshal oauth token request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/open-apis/authen/v2/oauth/token", bytes.NewReader(body))
	if err != nil {
		return OAuthTokenResult{}, fmt.Errorf("create oauth token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return OAuthTokenResult{}, fmt.Errorf("request oauth token: %w", err)
	}
	defer resp.Body.Close()

	var payload struct {
		Code                  int    `json:"code"`
		Msg                   string `json:"msg"`
		AccessToken           string `json:"access_token"`
		ExpiresIn             int    `json:"expires_in"`
		RefreshToken          string `json:"refresh_token"`
		RefreshTokenExpiresIn int    `json:"refresh_token_expires_in"`
		Data                  struct {
			AccessToken      string `json:"access_token"`
			ExpiresIn        int    `json:"expires_in"`
			Name             string `json:"name"`
			EnName           string `json:"en_name"`
			AvatarURL        string `json:"avatar_url"`
			OpenID           string `json:"open_id"`
			UnionID          string `json:"union_id"`
			UserID           string `json:"user_id"`
			Email            string `json:"email"`
			TenantKey        string `json:"tenant_key"`
			RefreshToken     string `json:"refresh_token"`
			RefreshExpiresIn int    `json:"refresh_expires_in"`
		} `json:"data"`
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return OAuthTokenResult{}, fmt.Errorf("read oauth token response: %w", err)
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return OAuthTokenResult{}, fmt.Errorf("decode oauth token response: %w", err)
	}
	if resp.StatusCode >= 400 || payload.Code != 0 {
		return OAuthTokenResult{}, fmt.Errorf("oauth token failed: status=%d code=%d msg=%s body=%s", resp.StatusCode, payload.Code, payload.Msg, strings.TrimSpace(string(data)))
	}

	result := OAuthTokenResult{
		AccessToken:      firstNonEmpty(payload.AccessToken, payload.Data.AccessToken),
		RefreshToken:     firstNonEmpty(payload.RefreshToken, payload.Data.RefreshToken),
		ExpiresIn:        firstNonZero(payload.ExpiresIn, payload.Data.ExpiresIn),
		RefreshExpiresIn: firstNonZero(payload.RefreshTokenExpiresIn, payload.Data.RefreshExpiresIn),
		Name:             payload.Data.Name,
		EnName:           payload.Data.EnName,
		AvatarURL:        payload.Data.AvatarURL,
		OpenID:           payload.Data.OpenID,
		UnionID:          payload.Data.UnionID,
		UserID:           payload.Data.UserID,
		Email:            payload.Data.Email,
		TenantKey:        payload.Data.TenantKey,
	}
	if result.AccessToken == "" {
		return OAuthTokenResult{}, fmt.Errorf("oauth token response missing access_token: %s", strings.TrimSpace(string(data)))
	}
	if err := c.fillUserInfo(ctx, &result); err != nil {
		return result, nil
	}
	return result, nil
}

func (c *Client) fillUserInfo(ctx context.Context, result *OAuthTokenResult) error {
	if result.AccessToken == "" {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/open-apis/authen/v1/user_info", nil)
	if err != nil {
		return fmt.Errorf("create user info request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+result.AccessToken)

	var payload struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Name      string `json:"name"`
			EnName    string `json:"en_name"`
			AvatarURL string `json:"avatar_url"`
			OpenID    string `json:"open_id"`
			UnionID   string `json:"union_id"`
			UserID    string `json:"user_id"`
			Email     string `json:"email"`
			TenantKey string `json:"tenant_key"`
		} `json:"data"`
	}
	if err := c.doJSON(req, &payload); err != nil {
		return err
	}
	if payload.Code != 0 {
		return fmt.Errorf("user info failed: code=%d msg=%s", payload.Code, payload.Msg)
	}
	result.Name = firstNonEmpty(result.Name, payload.Data.Name)
	result.EnName = firstNonEmpty(result.EnName, payload.Data.EnName)
	result.AvatarURL = firstNonEmpty(result.AvatarURL, payload.Data.AvatarURL)
	result.OpenID = firstNonEmpty(result.OpenID, payload.Data.OpenID)
	result.UnionID = firstNonEmpty(result.UnionID, payload.Data.UnionID)
	result.UserID = firstNonEmpty(result.UserID, payload.Data.UserID)
	result.Email = firstNonEmpty(result.Email, payload.Data.Email)
	result.TenantKey = firstNonEmpty(result.TenantKey, payload.Data.TenantKey)
	return nil
}

func (c *Client) ListUserChats(ctx context.Context, userAccessToken string) ([]source.RemoteChat, error) {
	return c.listUserChats(ctx, userAccessToken, "group")
}

func (c *Client) ListUserP2PChats(ctx context.Context, userAccessToken string) ([]source.RemoteChat, error) {
	return c.listUserChats(ctx, userAccessToken, "p2p")
}

func (c *Client) listUserChats(ctx context.Context, userAccessToken string, chatType string) ([]source.RemoteChat, error) {
	values := url.Values{}
	values.Set("page_size", "50")
	values.Set("user_id_type", "open_id")
	values.Set("types", chatType)

	var items []source.RemoteChat
	for {
		reqURL := c.baseURL + "/open-apis/im/v1/chats?" + values.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create list chats request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+userAccessToken)

		var payload struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
			Data struct {
				Items []json.RawMessage `json:"items"`
				Token string            `json:"page_token"`
				More  bool              `json:"has_more"`
			} `json:"data"`
		}
		if err := c.doJSON(req, &payload); err != nil {
			return nil, err
		}
		if payload.Code != 0 {
			return nil, fmt.Errorf("list chats failed: code=%d msg=%s", payload.Code, payload.Msg)
		}

		for _, raw := range payload.Data.Items {
			var item struct {
				ChatID      string `json:"chat_id"`
				Name        string `json:"name"`
				Description string `json:"description"`
				ChatStatus  string `json:"chat_status"`
				ChatMode    string `json:"chat_mode"`
			}
			if err := json.Unmarshal(raw, &item); err != nil {
				return nil, fmt.Errorf("decode chat item: %w", err)
			}
			items = append(items, source.RemoteChat{
				ChatID:      item.ChatID,
				Name:        item.Name,
				Description: item.Description,
				ChatStatus:  item.ChatStatus,
				ChatMode:    item.ChatMode,
				RawPayload:  raw,
			})
		}

		if !payload.Data.More || payload.Data.Token == "" {
			break
		}
		values.Set("page_token", payload.Data.Token)
	}

	return items, nil
}

func (c *Client) ListHistoryMessages(ctx context.Context, accessToken string, chatID string, start time.Time, end time.Time) ([]source.RemoteMessage, error) {
	values := url.Values{}
	values.Set("container_id_type", "chat")
	values.Set("container_id", chatID)
	values.Set("start_time", fmt.Sprintf("%d", start.Unix()))
	values.Set("end_time", fmt.Sprintf("%d", end.Unix()))
	values.Set("page_size", "50")

	items := make([]source.RemoteMessage, 0)
	for {
		reqURL := c.baseURL + "/open-apis/im/v1/messages?" + values.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create list history messages request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)

		var payload struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
			Data struct {
				Items []json.RawMessage `json:"items"`
				Token string            `json:"page_token"`
				More  bool              `json:"has_more"`
			} `json:"data"`
		}
		if err := c.doJSON(req, &payload); err != nil {
			return nil, err
		}
		if payload.Code != 0 {
			return nil, fmt.Errorf("list history messages failed: code=%d msg=%s", payload.Code, payload.Msg)
		}

		for _, raw := range payload.Data.Items {
			item, err := parseHistoryMessage(raw)
			if err != nil {
				return nil, err
			}
			if item.MessageID != "" {
				items = append(items, item)
			}
		}

		if !payload.Data.More || payload.Data.Token == "" {
			break
		}
		values.Set("page_token", payload.Data.Token)
	}

	return items, nil
}

func (c *Client) SendTextMessage(ctx context.Context, accessToken string, receiveID string, text string) (string, error) {
	content, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return "", fmt.Errorf("marshal text message content: %w", err)
	}
	body, err := json.Marshal(map[string]string{
		"receive_id": receiveID,
		"msg_type":   "text",
		"content":    string(content),
	})
	if err != nil {
		return "", fmt.Errorf("marshal send message request: %w", err)
	}

	reqURL := c.baseURL + "/open-apis/im/v1/messages?receive_id_type=open_id"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create send message request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	var payload struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			ChatID string `json:"chat_id"`
		} `json:"data"`
	}
	if err := c.doJSON(req, &payload); err != nil {
		return "", err
	}
	if payload.Code != 0 {
		return "", fmt.Errorf("send message failed: code=%d msg=%s", payload.Code, payload.Msg)
	}
	if payload.Data.ChatID == "" {
		return "", fmt.Errorf("send message response missing chat_id")
	}
	return payload.Data.ChatID, nil
}

func (c *Client) ListDepartmentUsers(ctx context.Context, userAccessToken string, departmentID string) ([]source.RemoteContact, error) {
	values := url.Values{}
	values.Set("department_id", departmentID)
	values.Set("department_id_type", "open_department_id")
	values.Set("user_id_type", "open_id")
	values.Set("page_size", "50")

	var contacts []source.RemoteContact
	for {
		reqURL := c.baseURL + "/open-apis/contact/v3/users?" + values.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create list users request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+userAccessToken)

		var payload struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
			Data struct {
				Items []json.RawMessage `json:"items"`
				Token string            `json:"page_token"`
				More  bool              `json:"has_more"`
			} `json:"data"`
		}
		if err := c.doJSON(req, &payload); err != nil {
			return nil, err
		}
		if payload.Code != 0 {
			return nil, fmt.Errorf("list department users failed: code=%d msg=%s", payload.Code, payload.Msg)
		}

		for _, raw := range payload.Data.Items {
			var item struct {
				OpenID  string `json:"open_id"`
				UserID  string `json:"user_id"`
				UnionID string `json:"union_id"`
				Name    string `json:"name"`
				Email   string `json:"email"`
			}
			if err := json.Unmarshal(raw, &item); err != nil {
				return nil, fmt.Errorf("decode user item: %w", err)
			}
			contacts = append(contacts, source.RemoteContact{
				OpenID:     item.OpenID,
				UserID:     item.UserID,
				UnionID:    item.UnionID,
				Name:       item.Name,
				Email:      item.Email,
				RawPayload: raw,
			})
		}

		if !payload.Data.More || payload.Data.Token == "" {
			break
		}
		values.Set("page_token", payload.Data.Token)
	}

	return contacts, nil
}

func (c *Client) SearchUsers(ctx context.Context, userAccessToken string, query string) ([]source.RemoteContact, error) {
	values := url.Values{}
	values.Set("query", query)
	values.Set("page_size", "50")

	reqURL := c.baseURL + "/open-apis/search/v1/user?" + values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create search users request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+userAccessToken)

	var payload struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Users []json.RawMessage `json:"users"`
			Items []json.RawMessage `json:"items"`
		} `json:"data"`
	}
	if err := c.doJSON(req, &payload); err != nil {
		return nil, err
	}
	if payload.Code != 0 {
		return nil, fmt.Errorf("search users failed: code=%d msg=%s", payload.Code, payload.Msg)
	}

	rawItems := payload.Data.Users
	if len(rawItems) == 0 {
		rawItems = payload.Data.Items
	}

	contacts := make([]source.RemoteContact, 0, len(rawItems))
	for _, raw := range rawItems {
		var item struct {
			ID          string `json:"id"`
			OpenID      string `json:"open_id"`
			UserID      string `json:"user_id"`
			UnionID     string `json:"union_id"`
			Name        string `json:"name"`
			Email       string `json:"email"`
			ChatID      string `json:"chat_id"`
			DisplayInfo string `json:"display_info"`
			MetaData    struct {
				MailAddress           string            `json:"mail_address"`
				EnterpriseMailAddress string            `json:"enterprise_mail_address"`
				I18nNames             map[string]string `json:"i18n_names"`
				Description           string            `json:"description"`
			} `json:"meta_data"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, fmt.Errorf("decode searched user item: %w", err)
		}

		openID := item.OpenID
		if openID == "" {
			openID = item.ID
		}
		name := item.Name
		if name == "" {
			name = cleanSearchDisplayInfo(item.DisplayInfo)
		}
		if name == "" {
			name = firstI18nName(item.MetaData.I18nNames)
		}
		email := item.Email
		if email == "" {
			email = item.MetaData.EnterpriseMailAddress
		}
		if email == "" {
			email = item.MetaData.MailAddress
		}

		contacts = append(contacts, source.RemoteContact{
			OpenID:     openID,
			UserID:     item.UserID,
			UnionID:    item.UnionID,
			Name:       name,
			Email:      email,
			ChatID:     item.ChatID,
			RawPayload: raw,
		})
	}

	return contacts, nil
}

func (c *Client) doJSON(req *http.Request, target any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request %s: %w", req.URL.Path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read %s response: %w", req.URL.Path, err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode %s response: %w", req.URL.Path, err)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("request %s failed: status=%d body=%s", req.URL.Path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return nil
}

func parseHistoryMessage(raw json.RawMessage) (source.RemoteMessage, error) {
	var item struct {
		MessageID   string `json:"message_id"`
		ChatID      string `json:"chat_id"`
		SenderID    string `json:"sender_id"`
		MessageType string `json:"msg_type"`
		CreateTime  string `json:"create_time"`
		Sender      struct {
			ID       string `json:"id"`
			IDType   string `json:"id_type"`
			SenderID struct {
				UserID string `json:"user_id"`
				OpenID string `json:"open_id"`
			} `json:"sender_id"`
		} `json:"sender"`
		Body struct {
			Content json.RawMessage `json:"content"`
		} `json:"body"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		return source.RemoteMessage{}, fmt.Errorf("decode history message item: %w", err)
	}

	rawContent := item.Body.Content

	senderID := item.SenderID
	if senderID == "" {
		senderID = item.Sender.ID
	}
	if senderID == "" {
		senderID = item.Sender.SenderID.OpenID
	}
	if senderID == "" {
		senderID = item.Sender.SenderID.UserID
	}

	var sentAt *time.Time
	if item.CreateTime != "" {
		if parsed, err := parseFeishuMillis(item.CreateTime); err == nil {
			sentAt = &parsed
		}
	}

	return source.RemoteMessage{
		MessageID:   item.MessageID,
		ChatID:      item.ChatID,
		SenderID:    senderID,
		MessageType: item.MessageType,
		ContentText: extractTextContent(item.MessageType, rawContent),
		RawContent:  rawContent,
		RawPayload:  raw,
		SentAt:      sentAt,
	}, nil
}

func cleanSearchDisplayInfo(value string) string {
	replacer := strings.NewReplacer("<h>", "", "</h>", "")
	return strings.TrimSpace(replacer.Replace(value))
}

func firstI18nName(names map[string]string) string {
	for _, key := range []string{"zh_cn", "en_us", "ja_jp"} {
		if names[key] != "" {
			return names[key]
		}
	}
	for _, value := range names {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}
