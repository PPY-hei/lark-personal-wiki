package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL          string
	apiKey           string
	model            string
	visionBaseURL    string
	visionAPIKey     string
	visionModel      string
	embeddingBaseURL string
	embeddingAPIKey  string
	embeddingModel   string
	embeddingDims    int
	http             *http.Client
}

func NewClient(baseURL string, apiKey string, model string, embeddingBaseURL string, embeddingAPIKey string, embeddingModel string, embeddingDims int) *Client {
	return NewClientWithVision(baseURL, apiKey, model, embeddingBaseURL, embeddingAPIKey, embeddingModel, embeddingDims, "", "", "")
}

func NewClientWithVision(baseURL string, apiKey string, model string, embeddingBaseURL string, embeddingAPIKey string, embeddingModel string, embeddingDims int, visionBaseURL string, visionAPIKey string, visionModel string) *Client {
	baseURL = normalizeBaseURL(baseURL)
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	visionBaseURL = normalizeBaseURL(visionBaseURL)
	if visionBaseURL == "" {
		visionBaseURL = baseURL
	}
	if visionAPIKey == "" {
		visionAPIKey = apiKey
	}
	embeddingBaseURL = normalizeBaseURL(embeddingBaseURL)
	if embeddingBaseURL == "" {
		embeddingBaseURL = baseURL
	}
	if embeddingAPIKey == "" {
		embeddingAPIKey = apiKey
	}
	if model == "" {
		model = "gpt-5.5"
	}
	if visionModel == "" {
		visionModel = model
	}
	if embeddingModel == "" {
		embeddingModel = "text-embedding-3-small"
	}
	if embeddingDims < 0 {
		embeddingDims = 0
	}
	return &Client{
		baseURL:          baseURL,
		apiKey:           apiKey,
		model:            model,
		visionBaseURL:    visionBaseURL,
		visionAPIKey:     visionAPIKey,
		visionModel:      visionModel,
		embeddingBaseURL: embeddingBaseURL,
		embeddingAPIKey:  embeddingAPIKey,
		embeddingModel:   embeddingModel,
		embeddingDims:    embeddingDims,
		http:             &http.Client{Timeout: 60 * time.Second},
	}
}

func normalizeBaseURL(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return ""
	}
	if strings.HasSuffix(baseURL, "/v1") {
		return baseURL
	}
	return baseURL + "/v1"
}

func (c *Client) Model() string {
	return c.model
}

func (c *Client) Embed(ctx context.Context, input string) ([]float32, error) {
	if c.embeddingAPIKey == "" {
		return nil, fmt.Errorf("OPENAI_EMBEDDING_API_KEY or DASHSCOPE_API_KEY is required")
	}
	requestBody := map[string]any{
		"model": c.embeddingModel,
		"input": input,
	}
	if c.embeddingDims > 0 {
		requestBody["dimensions"] = c.embeddingDims
	}
	body, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("marshal embedding request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.embeddingBaseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create embedding request: %w", err)
	}
	c.setHeaders(req, c.embeddingAPIKey)

	var payload struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
		Error apiError `json:"error"`
	}
	if err := c.do(req, &payload); err != nil {
		return nil, err
	}
	if payload.Error.Message != "" {
		return nil, fmt.Errorf("embedding failed: %s", payload.Error.Message)
	}
	if len(payload.Data) == 0 || len(payload.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("embedding response missing vector")
	}
	return payload.Data[0].Embedding, nil
}

func (c *Client) GenerateAnswer(ctx context.Context, question string, contextText string) (string, error) {
	if c.apiKey == "" {
		return "", fmt.Errorf("OPENAI_API_KEY is required")
	}
	prompt := buildPrompt(question, contextText)
	body, err := json.Marshal(map[string]any{
		"model": c.model,
		"input": prompt,
	})
	if err != nil {
		return "", fmt.Errorf("marshal response request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create response request: %w", err)
	}
	c.setHeaders(req, c.apiKey)

	var payload struct {
		OutputText string `json:"output_text"`
		Output     []struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Error apiError `json:"error"`
	}
	if err := c.do(req, &payload); err != nil {
		return "", err
	}
	if payload.Error.Message != "" {
		return "", fmt.Errorf("response failed: %s", payload.Error.Message)
	}
	if strings.TrimSpace(payload.OutputText) != "" {
		return strings.TrimSpace(payload.OutputText), nil
	}
	for _, output := range payload.Output {
		for _, content := range output.Content {
			if strings.TrimSpace(content.Text) != "" {
				return strings.TrimSpace(content.Text), nil
			}
		}
	}
	return "", fmt.Errorf("response missing output text")
}

func (c *Client) DescribeImage(ctx context.Context, mimeType string, imageBytes []byte, hint string) (string, error) {
	if c.visionAPIKey == "" {
		return "", fmt.Errorf("VISION_API_KEY or OPENAI_API_KEY is required")
	}
	if len(imageBytes) == 0 {
		return "", fmt.Errorf("image bytes are required")
	}
	dataURL := "data:" + firstNonEmpty(mimeType, "image/jpeg") + ";base64," + base64Encode(imageBytes)
	prompt := `请理解这张飞书聊天图片，输出可用于知识库检索的中文文本。

要求：
1. 如果图片里有文字，完整提取 OCR 文本。
2. 描述图片中的关键对象、界面、错误信息、配置、表格、数字和结论。
3. 如果是截图，说明它像是什么系统或页面，以及可见的状态。
4. 不要编造看不见的信息。
5. 输出格式固定为：
OCR文本：
...

图片描述：
...

关键信息：
...`
	if strings.TrimSpace(hint) != "" {
		prompt += "\n\n聊天上下文提示：\n" + strings.TrimSpace(hint)
	}
	body, err := json.Marshal(map[string]any{
		"model": c.visionModel,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "text", "text": prompt},
					{"type": "image_url", "image_url": map[string]any{"url": dataURL}},
				},
			},
		},
		"max_tokens": 1000,
	})
	if err != nil {
		return "", fmt.Errorf("marshal vision request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.visionBaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create vision request: %w", err)
	}
	c.setHeaders(req, c.visionAPIKey)

	var payload struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error apiError `json:"error"`
	}
	if err := c.do(req, &payload); err != nil {
		return "", err
	}
	if payload.Error.Message != "" {
		return "", fmt.Errorf("vision failed: %s", payload.Error.Message)
	}
	if len(payload.Choices) == 0 || strings.TrimSpace(payload.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("vision response missing content")
	}
	return strings.TrimSpace(payload.Choices[0].Message.Content), nil
}

func (c *Client) setHeaders(req *http.Request, apiKey string) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
}

func (c *Client) do(req *http.Request, target any) error {
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
		return fmt.Errorf("decode %s response: %w body=%s", req.URL.Path, err, strings.TrimSpace(string(data)))
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("request %s failed: status=%d body=%s", req.URL.Path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return nil
}

type apiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

func base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func buildPrompt(question string, contextText string) string {
	return `你是一个严谨的个人飞书知识库助手。你只能基于给定的聊天记录上下文回答。

要求：
1. 如果上下文不足以回答，明确说“当前知识库没有足够信息”。
2. 回答要先给结论，再给依据。
3. 涉及代码、配置、决策时，保留关键原文。
4. 不要编造聊天记录中没有出现的信息。
5. 最后列出“参考来源”，必须写出上下文来源标记里的可读名称、日期和片段，例如“来源 1：华望私有化 / 2026-06-01 / 片段 1”。不要只写“来源 1”。

问题：
` + question + `

聊天记录上下文：
` + contextText
}
