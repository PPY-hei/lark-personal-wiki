package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type AIClient interface {
	Embed(ctx context.Context, input string) ([]float32, error)
	GenerateAnswer(ctx context.Context, question string, contextText string) (string, error)
	Model() string
}

type Service struct {
	db            *pgxpool.Pool
	ai            AIClient
	useEmbeddings bool
}

type IndexResult struct {
	Units  int `json:"units"`
	Chunks int `json:"chunks"`
}

type AskResult struct {
	Question string           `json:"question"`
	Answer   string           `json:"answer"`
	Sources  []RetrievedChunk `json:"sources"`
}

type RetrievedChunk struct {
	ID         int64           `json:"id"`
	SourceID   string          `json:"source_id"`
	Content    string          `json:"content"`
	Score      float64         `json:"score"`
	Metadata   json.RawMessage `json:"metadata"`
	CreatedAt  time.Time       `json:"created_at"`
	SourceType string          `json:"source_type"`
}

type unitCandidate struct {
	FeishuChatID string
	UnitDate     time.Time
	Content      string
	MessageIDs   []int64
}

func NewService(db *pgxpool.Pool, ai AIClient, useEmbeddings bool) *Service {
	return &Service{db: db, ai: ai, useEmbeddings: useEmbeddings}
}

func (s *Service) BuildIndex(ctx context.Context, days int) (IndexResult, error) {
	if days <= 0 {
		days = 30
	}
	units, err := s.loadUnitCandidates(ctx, days)
	if err != nil {
		return IndexResult{}, err
	}
	result := IndexResult{}
	for _, unit := range units {
		unitID, sourceID, err := s.upsertUnit(ctx, unit)
		if err != nil {
			return result, err
		}
		if err := s.replaceUnitMessages(ctx, unitID, unit.MessageIDs); err != nil {
			return result, err
		}
		result.Units++

		chunks := splitContent(unit.Content, 3200, 400)
		for idx, content := range chunks {
			var embedding []float32
			if s.useEmbeddings {
				embedding, err = s.ai.Embed(ctx, content)
				if err != nil {
					return result, fmt.Errorf("embed chunk %s:%d: %w", sourceID, idx, err)
				}
			}
			if err := s.upsertChunk(ctx, unitID, sourceID, idx, content, embedding, unit); err != nil {
				return result, err
			}
			result.Chunks++
		}
	}
	return result, nil
}

func (s *Service) Ask(ctx context.Context, question string, limit int) (AskResult, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return AskResult{}, fmt.Errorf("question is required")
	}
	if limit <= 0 {
		limit = 8
	}
	chunks, err := s.Retrieve(ctx, question, limit)
	if err != nil {
		return AskResult{}, err
	}
	contextText := buildContext(chunks)
	answer, err := s.ai.GenerateAnswer(ctx, question, contextText)
	if err != nil {
		_ = s.saveQALog(ctx, question, "", chunks, "failed", err)
		return AskResult{}, err
	}
	if err := s.saveQALog(ctx, question, answer, chunks, "answered", nil); err != nil {
		return AskResult{}, err
	}
	return AskResult{Question: question, Answer: answer, Sources: chunks}, nil
}

func (s *Service) Retrieve(ctx context.Context, question string, limit int) ([]RetrievedChunk, error) {
	if s.useEmbeddings {
		embedding, err := s.ai.Embed(ctx, question)
		if err != nil {
			return nil, err
		}
		return s.SearchByEmbedding(ctx, embedding, limit)
	}
	return s.SearchByText(ctx, question, limit)
}

func (s *Service) SearchByEmbedding(ctx context.Context, embedding []float32, limit int) ([]RetrievedChunk, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, source_type, source_id, content, metadata, created_at, 1 - (embedding <=> $1::vector) AS score
		FROM knowledge_chunks
		WHERE embedding IS NOT NULL
		ORDER BY embedding <=> $1::vector
		LIMIT $2
	`, vectorLiteral(embedding), limit)
	if err != nil {
		return nil, fmt.Errorf("search chunks: %w", err)
	}
	defer rows.Close()
	return scanChunks(rows)
}

func (s *Service) SearchByText(ctx context.Context, question string, limit int) ([]RetrievedChunk, error) {
	keywords := extractKeywords(question)
	query := strings.Join(keywords, " | ")
	if strings.TrimSpace(query) == "" {
		query = question
	}
	rows, err := s.db.Query(ctx, `
		WITH q AS (
			SELECT websearch_to_tsquery('simple', $1) AS tsq
		)
		SELECT id, source_type, source_id, content, metadata, created_at,
		       ts_rank_cd(to_tsvector('simple', content), q.tsq) AS score
		FROM knowledge_chunks, q
		WHERE to_tsvector('simple', content) @@ q.tsq
		ORDER BY score DESC, updated_at DESC
		LIMIT $2
	`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("text search chunks: %w", err)
	}
	defer rows.Close()

	items, err := scanChunks(rows)
	if err != nil {
		return nil, err
	}
	if len(items) > 0 {
		return items, nil
	}
	return s.SearchRecent(ctx, limit)
}

func (s *Service) SearchRecent(ctx context.Context, limit int) ([]RetrievedChunk, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, source_type, source_id, content, metadata, created_at, 0::float8 AS score
		FROM knowledge_chunks
		ORDER BY updated_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("recent chunks: %w", err)
	}
	defer rows.Close()
	return scanChunks(rows)
}

func (s *Service) loadUnitCandidates(ctx context.Context, days int) ([]unitCandidate, error) {
	rows, err := s.db.Query(ctx, `
		SELECT
			feishu_chat_id,
			(coalesce(sent_at, created_at) AT TIME ZONE 'Asia/Shanghai')::date AS unit_date,
			string_agg(
				'[' || to_char(coalesce(sent_at, created_at) AT TIME ZONE 'Asia/Shanghai', 'YYYY-MM-DD HH24:MI') || '] ' ||
				coalesce(nullif(feishu_sender_id, ''), 'unknown') || ': ' || content_text,
				E'\n' ORDER BY coalesce(sent_at, created_at), id
			) AS content,
			array_agg(id ORDER BY coalesce(sent_at, created_at), id) AS message_ids
		FROM messages
		WHERE nullif(trim(content_text), '') IS NOT NULL
		  AND coalesce(sent_at, created_at) >= now() - ($1::int * interval '1 day')
		GROUP BY feishu_chat_id, unit_date
		ORDER BY unit_date DESC, feishu_chat_id
	`, days)
	if err != nil {
		return nil, fmt.Errorf("load unit candidates: %w", err)
	}
	defer rows.Close()

	items := make([]unitCandidate, 0)
	for rows.Next() {
		var item unitCandidate
		if err := rows.Scan(&item.FeishuChatID, &item.UnitDate, &item.Content, &item.MessageIDs); err != nil {
			return nil, fmt.Errorf("scan unit candidate: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate unit candidates: %w", err)
	}
	return items, nil
}

func (s *Service) upsertUnit(ctx context.Context, unit unitCandidate) (int64, string, error) {
	sourceID := unit.FeishuChatID + ":" + unit.UnitDate.Format("2006-01-02")
	metadata, _ := json.Marshal(map[string]any{
		"feishu_chat_id": unit.FeishuChatID,
		"unit_date":      unit.UnitDate.Format("2006-01-02"),
		"message_count":  len(unit.MessageIDs),
	})
	var id int64
	err := s.db.QueryRow(ctx, `
		INSERT INTO knowledge_units (source_type, source_id, chat_id, unit_date, title, content, metadata, updated_at)
		VALUES (
			'chat_day',
			$1,
			(SELECT id FROM chats WHERE feishu_chat_id=$2),
			$3,
			$4,
			$5,
			$6,
			now()
		)
		ON CONFLICT (source_type, source_id) DO UPDATE SET
			content = EXCLUDED.content,
			metadata = EXCLUDED.metadata,
			updated_at = now()
		RETURNING id
	`, sourceID, unit.FeishuChatID, unit.UnitDate, "飞书聊天 "+unit.UnitDate.Format("2006-01-02"), unit.Content, metadata).Scan(&id)
	if err != nil {
		return 0, "", fmt.Errorf("upsert knowledge unit: %w", err)
	}
	return id, sourceID, nil
}

func (s *Service) replaceUnitMessages(ctx context.Context, unitID int64, messageIDs []int64) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin unit messages: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM knowledge_unit_messages WHERE knowledge_unit_id=$1`, unitID); err != nil {
		return fmt.Errorf("delete unit messages: %w", err)
	}
	for _, messageID := range messageIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO knowledge_unit_messages (knowledge_unit_id, message_id)
			VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, unitID, messageID); err != nil {
			return fmt.Errorf("insert unit message: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit unit messages: %w", err)
	}
	return nil
}

func (s *Service) upsertChunk(ctx context.Context, unitID int64, unitSourceID string, index int, content string, embedding []float32, unit unitCandidate) error {
	sourceID := fmt.Sprintf("%s:%03d", unitSourceID, index)
	metadata, _ := json.Marshal(map[string]any{
		"knowledge_unit_id": unitID,
		"chunk_index":       index,
		"feishu_chat_id":    unit.FeishuChatID,
		"unit_date":         unit.UnitDate.Format("2006-01-02"),
	})
	embeddingValue := any(nil)
	if len(embedding) > 0 {
		embeddingValue = vectorLiteral(embedding)
	}
	if _, err := s.db.Exec(ctx, `
		INSERT INTO knowledge_chunks (source_type, source_id, chat_id, content, embedding, token_count, metadata, visibility_scope, updated_at)
		VALUES (
			'knowledge_unit',
			$1,
			(SELECT id FROM chats WHERE feishu_chat_id=$2),
			$3,
			CASE WHEN $4::text IS NULL THEN NULL ELSE $4::vector END,
			$5,
			$6,
			'current_chat',
			now()
		)
		ON CONFLICT (source_type, source_id) DO UPDATE SET
			content = EXCLUDED.content,
			embedding = EXCLUDED.embedding,
			token_count = EXCLUDED.token_count,
			metadata = EXCLUDED.metadata,
			updated_at = now()
	`, sourceID, unit.FeishuChatID, content, embeddingValue, estimateTokens(content), metadata); err != nil {
		return fmt.Errorf("upsert knowledge chunk: %w", err)
	}
	return nil
}

type chunkRows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}

func scanChunks(rows chunkRows) ([]RetrievedChunk, error) {
	items := make([]RetrievedChunk, 0)
	for rows.Next() {
		var item RetrievedChunk
		if err := rows.Scan(&item.ID, &item.SourceType, &item.SourceID, &item.Content, &item.Metadata, &item.CreatedAt, &item.Score); err != nil {
			return nil, fmt.Errorf("scan retrieved chunk: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate retrieved chunks: %w", err)
	}
	return items, nil
}

func (s *Service) saveQALog(ctx context.Context, question string, answer string, chunks []RetrievedChunk, status string, qaErr error) error {
	chunkJSON, _ := json.Marshal(chunks)
	errText := ""
	answeredAt := any(nil)
	if qaErr != nil {
		errText = qaErr.Error()
	}
	if status == "answered" {
		answeredAt = time.Now()
	}
	if _, err := s.db.Exec(ctx, `
		INSERT INTO qa_logs (question, answer, model, retrieved_chunks, status, error, answered_at)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7)
	`, question, answer, s.ai.Model(), chunkJSON, status, errText, answeredAt); err != nil {
		return fmt.Errorf("save qa log: %w", err)
	}
	return nil
}

func splitContent(content string, maxRunes int, overlap int) []string {
	runes := []rune(strings.TrimSpace(content))
	if len(runes) == 0 {
		return nil
	}
	if maxRunes <= 0 {
		maxRunes = 3200
	}
	if overlap < 0 || overlap >= maxRunes {
		overlap = 0
	}
	chunks := make([]string, 0, int(math.Ceil(float64(len(runes))/float64(maxRunes))))
	for start := 0; start < len(runes); {
		end := start + maxRunes
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, strings.TrimSpace(string(runes[start:end])))
		if end == len(runes) {
			break
		}
		start = end - overlap
	}
	return chunks
}

func vectorLiteral(values []float32) string {
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = fmt.Sprintf("%g", value)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func estimateTokens(content string) int {
	return len([]rune(content)) / 2
}

func buildContext(chunks []RetrievedChunk) string {
	var builder strings.Builder
	for idx, chunk := range chunks {
		_, _ = fmt.Fprintf(&builder, "\n[来源 %d | score %.4f | %s]\n%s\n", idx+1, chunk.Score, chunk.SourceID, chunk.Content)
	}
	if builder.Len() == 0 {
		return "没有检索到相关聊天记录。"
	}
	return builder.String()
}

var keywordPattern = regexp.MustCompile(`[\p{Han}A-Za-z0-9_./:-]+`)

func extractKeywords(question string) []string {
	raw := keywordPattern.FindAllString(question, -1)
	seen := make(map[string]bool)
	keywords := make([]string, 0, len(raw))
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if len([]rune(item)) < 2 {
			continue
		}
		key := strings.ToLower(item)
		if seen[key] {
			continue
		}
		seen[key] = true
		keywords = append(keywords, item)
	}
	if len(keywords) > 8 {
		return keywords[:8]
	}
	return keywords
}
