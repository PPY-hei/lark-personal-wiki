package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	return c.baseURL + "/open-apis/authen/v1/index?redirect_uri=" + url.QueryEscape(c.redirectURI) + "&app_id=" + url.QueryEscape(c.appID) + "&state=" + url.QueryEscape(state)
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
	appToken, err := c.AppAccessToken(ctx)
	if err != nil {
		return OAuthTokenResult{}, err
	}

	body, err := json.Marshal(map[string]string{
		"grant_type": "authorization_code",
		"code":       code,
	})
	if err != nil {
		return OAuthTokenResult{}, fmt.Errorf("marshal oauth token request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/open-apis/authen/v1/access_token", bytes.NewReader(body))
	if err != nil {
		return OAuthTokenResult{}, fmt.Errorf("create oauth token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+appToken)

	resp, err := c.http.Do(req)
	if err != nil {
		return OAuthTokenResult{}, fmt.Errorf("request oauth token: %w", err)
	}
	defer resp.Body.Close()

	var payload struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
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
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return OAuthTokenResult{}, fmt.Errorf("decode oauth token response: %w", err)
	}
	if resp.StatusCode >= 400 || payload.Code != 0 {
		return OAuthTokenResult{}, fmt.Errorf("oauth token failed: status=%d code=%d msg=%s", resp.StatusCode, payload.Code, payload.Msg)
	}

	return OAuthTokenResult{
		AccessToken:      payload.Data.AccessToken,
		RefreshToken:     payload.Data.RefreshToken,
		ExpiresIn:        payload.Data.ExpiresIn,
		RefreshExpiresIn: payload.Data.RefreshExpiresIn,
		Name:             payload.Data.Name,
		EnName:           payload.Data.EnName,
		AvatarURL:        payload.Data.AvatarURL,
		OpenID:           payload.Data.OpenID,
		UnionID:          payload.Data.UnionID,
		UserID:           payload.Data.UserID,
		Email:            payload.Data.Email,
		TenantKey:        payload.Data.TenantKey,
	}, nil
}

func (c *Client) ListUserChats(ctx context.Context, userAccessToken string) ([]source.RemoteChat, error) {
	values := url.Values{}
	values.Set("page_size", "50")
	values.Set("user_id_type", "open_id")
	values.Set("types", "group")

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
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return fmt.Errorf("decode %s response: %w", req.URL.Path, err)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("request %s failed: status=%d", req.URL.Path, resp.StatusCode)
	}
	return nil
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
