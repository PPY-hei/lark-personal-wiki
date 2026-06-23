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
	webSearchEnabled bool
	webSearchTool    string
	http             *http.Client
}

func NewClient(baseURL string, apiKey string, model string, embeddingBaseURL string, embeddingAPIKey string, embeddingModel string, embeddingDims int) *Client {
	return NewClientWithVision(baseURL, apiKey, model, embeddingBaseURL, embeddingAPIKey, embeddingModel, embeddingDims, "", "", "")
}

func NewClientWithVision(baseURL string, apiKey string, model string, embeddingBaseURL string, embeddingAPIKey string, embeddingModel string, embeddingDims int, visionBaseURL string, visionAPIKey string, visionModel string) *Client {
	return NewClientWithOptions(ClientOptions{
		BaseURL:          baseURL,
		APIKey:           apiKey,
		Model:            model,
		EmbeddingBaseURL: embeddingBaseURL,
		EmbeddingAPIKey:  embeddingAPIKey,
		EmbeddingModel:   embeddingModel,
		EmbeddingDims:    embeddingDims,
		VisionBaseURL:    visionBaseURL,
		VisionAPIKey:     visionAPIKey,
		VisionModel:      visionModel,
	})
}

type ClientOptions struct {
	BaseURL          string
	APIKey           string
	Model            string
	EmbeddingBaseURL string
	EmbeddingAPIKey  string
	EmbeddingModel   string
	EmbeddingDims    int
	VisionBaseURL    string
	VisionAPIKey     string
	VisionModel      string
	WebSearchEnabled bool
	WebSearchTool    string
}

func NewClientWithOptions(options ClientOptions) *Client {
	baseURL := options.BaseURL
	apiKey := options.APIKey
	model := options.Model
	embeddingBaseURL := options.EmbeddingBaseURL
	embeddingAPIKey := options.EmbeddingAPIKey
	embeddingModel := options.EmbeddingModel
	embeddingDims := options.EmbeddingDims
	visionBaseURL := options.VisionBaseURL
	visionAPIKey := options.VisionAPIKey
	visionModel := options.VisionModel
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
	webSearchTool := strings.TrimSpace(options.WebSearchTool)
	if webSearchTool == "" {
		webSearchTool = "web_search"
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
		webSearchEnabled: options.WebSearchEnabled,
		webSearchTool:    webSearchTool,
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
	return c.createTextResponse(ctx, prompt, nil)
}

func (c *Client) ShouldUseWebSearch(ctx context.Context, question string, contextText string) (bool, string, error) {
	if c.apiKey == "" {
		return false, "", fmt.Errorf("OPENAI_API_KEY is required")
	}
	if !c.webSearchEnabled {
		return false, "web search disabled", nil
	}
	prompt := buildWebSearchDecisionPrompt(question, contextText)
	text, err := c.createTextResponse(ctx, prompt, nil)
	if err != nil {
		return false, "", err
	}
	decision, reason, err := parseWebSearchDecision(text)
	if err != nil {
		return false, "", err
	}
	return decision, reason, nil
}

func (c *Client) GenerateAnswerWithWebSearch(ctx context.Context, question string, contextText string) (string, error) {
	if c.apiKey == "" {
		return "", fmt.Errorf("OPENAI_API_KEY is required")
	}
	if !c.webSearchEnabled {
		return c.GenerateAnswer(ctx, question, contextText)
	}
	prompt := buildWebSearchPrompt(question, contextText)
	return c.createTextResponse(ctx, prompt, []map[string]any{{"type": c.webSearchTool}})
}

func (c *Client) createTextResponse(ctx context.Context, prompt string, tools []map[string]any) (string, error) {
	requestBody := map[string]any{
		"model": c.model,
		"input": prompt,
	}
	if len(tools) > 0 {
		requestBody["tools"] = tools
	}
	body, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("marshal response request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create response request: %w", err)
	}
	c.setHeaders(req, c.apiKey)

	var payload struct {
		OutputText string           `json:"output_text"`
		Output     []responseOutput `json:"output"`
		Error      apiError         `json:"error"`
	}
	if err := c.do(req, &payload); err != nil {
		return "", err
	}
	if payload.Error.Message != "" {
		return "", fmt.Errorf("response failed: %s", payload.Error.Message)
	}
	citations := collectURLCitations(payload.Output)
	if strings.TrimSpace(payload.OutputText) != "" {
		return appendURLCitations(strings.TrimSpace(payload.OutputText), citations), nil
	}
	for _, output := range payload.Output {
		for _, content := range output.Content {
			if strings.TrimSpace(content.Text) != "" {
				return appendURLCitations(strings.TrimSpace(content.Text), citations), nil
			}
		}
	}
	return "", fmt.Errorf("response missing output text")
}

func (c *Client) ExpandSearchKeywords(ctx context.Context, question string) ([]string, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY is required")
	}
	prompt := buildKeywordPrompt(question)
	body, err := json.Marshal(map[string]any{
		"model": c.model,
		"input": prompt,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal keyword expansion request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create keyword expansion request: %w", err)
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
		return nil, err
	}
	if payload.Error.Message != "" {
		return nil, fmt.Errorf("keyword expansion failed: %s", payload.Error.Message)
	}
	text := strings.TrimSpace(payload.OutputText)
	if text == "" {
		for _, output := range payload.Output {
			for _, content := range output.Content {
				if strings.TrimSpace(content.Text) != "" {
					text = strings.TrimSpace(content.Text)
					break
				}
			}
			if text != "" {
				break
			}
		}
	}
	if text == "" {
		return nil, fmt.Errorf("keyword expansion response missing output text")
	}
	return parseKeywordExpansion(text)
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

type responseOutput struct {
	Content []struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Annotations []struct {
			Type        string `json:"type"`
			URLCitation struct {
				URL   string `json:"url"`
				Title string `json:"title"`
			} `json:"url_citation"`
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"annotations"`
	} `json:"content"`
}

type urlCitation struct {
	Title string
	URL   string
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

func buildWebSearchDecisionPrompt(question string, contextText string) string {
	return `你是个人知识库助手的联网搜索决策器。请判断回答用户问题时是否需要联网搜索。

只在这些情况返回 true：
1. 问题明显需要最新信息、当前状态、价格、新闻、版本、官方文档、网页资料或外部事实。
2. 给定的聊天记录上下文不足以回答，并且问题不是只问个人历史聊天记录。
3. 用户明确要求搜索、查一下网上、最新、今天、现在、官网、文档。

这些情况返回 false：
1. 聊天记录上下文已经足够回答。
2. 问题主要是在问个人聊天记录、内部项目、账号、路径、历史结论。
3. 联网搜索也不太可能知道内部私有信息。

返回严格 JSON，不要 markdown，不要解释：
{"needs_web_search":true,"reason":"..."}

用户问题：
` + question + `

已检索到的个人知识库上下文：
` + contextText
}

func buildWebSearchPrompt(question string, contextText string) string {
	return `你是一个严谨的个人飞书知识库助手。你可以基于个人知识库上下文和联网搜索结果回答。

要求：
1. 优先使用个人知识库上下文；联网结果只用于补充最新或外部事实。
2. 如果个人知识库和联网结果冲突，明确说明冲突，不要强行合并。
3. 回答要先给结论，再给依据。
4. 涉及代码、配置、账号、内部项目时，必须以个人知识库为准，不要用网上内容编造。
5. 最后列出“参考来源”：个人知识库来源要写上下文来源标记里的可读名称、日期和片段；联网来源要写网页标题或域名。

问题：
` + question + `

个人知识库上下文：
` + contextText
}

func buildKeywordPrompt(question string) string {
	return `你是飞书个人知识库的检索词规划器。请把用户问题扩展成适合全文检索的关键词。

要求：
1. 返回严格 JSON，不要 markdown，不要解释。
2. JSON 格式：{"keywords":["..."]}。
3. 包含原始问题里的关键实体、英文技术词、缩写、配置字段、路径片段、同义词。
4. 中英文混合词要拆出英文技术词，例如“hive的jdbc路径”应包含 "hive"、"jdbc"、"jdbc:hive2"。
5. 不要返回太泛的词，比如“什么”、“怎么”、“路径”、“配置”，除非和技术词组合。
6. 最多 12 个关键词。

用户问题：
` + question
}

func parseKeywordExpansion(text string) ([]string, error) {
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	text = strings.TrimSpace(text)

	var payload struct {
		Keywords []string `json:"keywords"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		var raw []string
		if err2 := json.Unmarshal([]byte(text), &raw); err2 != nil {
			return nil, fmt.Errorf("decode keyword expansion: %w", err)
		}
		payload.Keywords = raw
	}
	keywords := make([]string, 0, len(payload.Keywords))
	seen := make(map[string]bool)
	for _, keyword := range payload.Keywords {
		keyword = strings.TrimSpace(keyword)
		if keyword == "" {
			continue
		}
		key := strings.ToLower(keyword)
		if seen[key] {
			continue
		}
		seen[key] = true
		keywords = append(keywords, keyword)
		if len(keywords) >= 12 {
			break
		}
	}
	return keywords, nil
}

func parseWebSearchDecision(text string) (bool, string, error) {
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	text = strings.TrimSpace(text)
	var payload struct {
		NeedsWebSearch bool   `json:"needs_web_search"`
		Reason         string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return false, "", fmt.Errorf("decode web search decision: %w", err)
	}
	return payload.NeedsWebSearch, strings.TrimSpace(payload.Reason), nil
}

func collectURLCitations(outputs []responseOutput) []urlCitation {
	citations := make([]urlCitation, 0)
	seen := make(map[string]bool)
	for _, output := range outputs {
		for _, content := range output.Content {
			for _, annotation := range content.Annotations {
				url := strings.TrimSpace(firstNonEmpty(annotation.URLCitation.URL, annotation.URL))
				if url == "" || seen[url] {
					continue
				}
				seen[url] = true
				citations = append(citations, urlCitation{
					Title: firstNonEmpty(annotation.URLCitation.Title, annotation.Title, url),
					URL:   url,
				})
			}
		}
	}
	return citations
}

func appendURLCitations(answer string, citations []urlCitation) string {
	answer = strings.TrimSpace(answer)
	if len(citations) == 0 {
		return answer
	}
	var builder strings.Builder
	builder.WriteString(answer)
	if !strings.Contains(answer, "联网来源") {
		builder.WriteString("\n\n联网来源：")
	}
	for idx, citation := range citations {
		if idx >= 5 {
			break
		}
		title := firstNonEmpty(citation.Title, citation.URL)
		builder.WriteString("\n- ")
		builder.WriteString(title)
		if citation.URL != "" && citation.URL != title {
			builder.WriteString("：")
			builder.WriteString(citation.URL)
		}
	}
	return builder.String()
}
