# Feishu KB Assistant MVP Roadmap

## 1. Product Boundary

MVP 目标是先做一个“飞书知识库机器人”，而不是直接做“个人账号全量代答”。

第一版只解决这些问题：

- 管理员选择哪些飞书群进入知识库。
- 系统同步这些群的历史消息和实时消息。
- 当群里有人 `@机器人` 提问时，机器人基于已同步知识生成回复。
- 回复必须可追溯来源，低置信度时不自动回复。
- 后台可查看群配置、同步状态、问答日志和命中来源。

第一版暂不做：

- 无感读取个人账号所有私聊。
- 以个人身份自动发消息。
- 跨租户 SaaS。
- 复杂权限模型。
- 多模态附件解析。
- 企业级审批流。

## 2. Technical Stack

### Backend

- Language: Go
- HTTP framework: Gin or Echo
- Database driver: pgx
- Redis client: go-redis
- Migration: golang-migrate or goose
- Background jobs: Asynq
- Config: envconfig or viper
- Logging: slog

Recommended MVP choice:

- Gin
- pgx
- go-redis
- goose
- Asynq
- slog

### Frontend

- Framework: Next.js
- UI: Ant Design
- API style: REST first

MVP 也可以先不做完整前端，先用后端 API + 简单管理页面推进。等核心链路跑通后，再补 Next.js 管理后台。

### Data Infrastructure

- PostgreSQL 16 + pgvector
- Redis 7.2
- Docker Compose for local development

Current local images:

- PostgreSQL/pgvector: `docker.m.daocloud.io/pgvector/pgvector:pg16`
- Redis: `docker.m.daocloud.io/library/redis:7.2-alpine`

### LLM and Embedding

MVP 需要抽象成 provider 接口，避免后续被某个模型绑定。

Required interfaces:

- `EmbeddingProvider`
- `ChatProvider`
- `RerankProvider` optional

First implementation can use:

- Embedding: OpenAI embeddings or bge-m3 compatible service
- Chat: OpenAI compatible chat completions API

## 3. MVP Iteration Path

## Step 1: Foundation and Feishu Event Ingestion

### Goal

建立 Go 服务基础骨架，能接收飞书事件，完成验签/解密/去重，并把消息原始事件保存到数据库。

这一步完成后，系统已经可以证明“飞书消息能进来”。

### Scope

- Go module 初始化。
- 配置加载。
- PostgreSQL / Redis 健康检查。
- 飞书 tenant access token 获取与缓存。
- 飞书事件订阅 endpoint。
- URL verification challenge 响应。
- 消息事件解析。
- 事件去重。
- 原始事件入库。

### Backend Modules

```text
cmd/server
  main.go

internal/config
  config.go

internal/http
  router.go
  middleware.go

internal/infra/postgres
  db.go

internal/infra/redis
  redis.go

internal/feishu
  client.go
  token.go
  event.go
  crypto.go

internal/message
  handler.go
  repository.go
  service.go
```

### Required Internal APIs

```http
GET /healthz
GET /readyz
POST /api/feishu/events
```

### Feishu Open Platform APIs

Use these official capabilities:

- Get tenant access token: `POST /open-apis/auth/v3/tenant_access_token/internal`
- Event subscription callback: configured in Feishu developer console.
- Receive message event: message event pushed by Feishu.

References:

- [Feishu API Authentication](https://open.feishu.cn/document/server-docs/authentication-management/access-token/tenant_access_token_internal)
- [Feishu Event Subscription](https://open.feishu.cn/document/server-docs/event-subscription-guide/overview)
- [Receive Message Event](https://open.feishu.cn/document/server-docs/im-v1/message/events/receive)

### Database Tables

```sql
CREATE TABLE feishu_events (
  id BIGSERIAL PRIMARY KEY,
  event_id TEXT NOT NULL UNIQUE,
  event_type TEXT NOT NULL,
  schema_version TEXT,
  raw_payload JSONB NOT NULL,
  received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  processed_at TIMESTAMPTZ,
  process_status TEXT NOT NULL DEFAULT 'pending',
  process_error TEXT
);

CREATE TABLE messages (
  id BIGSERIAL PRIMARY KEY,
  feishu_message_id TEXT NOT NULL UNIQUE,
  feishu_chat_id TEXT NOT NULL,
  feishu_sender_id TEXT,
  message_type TEXT NOT NULL,
  content_text TEXT,
  raw_content JSONB,
  raw_payload JSONB NOT NULL,
  sent_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_messages_chat_sent_at ON messages (feishu_chat_id, sent_at DESC);
```

### Redis Usage

```text
feishu:event:{event_id} -> 1, ttl 24h
feishu:tenant_access_token -> token, ttl from Feishu response minus safety window
```

### Acceptance Criteria

- `GET /healthz` returns OK.
- `GET /readyz` verifies PostgreSQL and Redis.
- Feishu URL verification can pass.
- A received message event is stored in `feishu_events`.
- A text message event is normalized into `messages`.
- Duplicate event delivery does not create duplicate messages.

## Step 2: Chat Configuration and History Sync

### Goal

管理员可以配置哪些群启用知识库；系统可以拉取指定群历史消息并入库。

这一步完成后，系统已经具备“指定群消息沉淀成知识源”的能力。

### Scope

- 群配置 CRUD。
- 历史同步任务创建。
- 飞书历史消息分页拉取。
- 消息标准化入库。
- 同步游标保存。
- 同步日志与失败重试。

### Required Internal APIs

```http
GET /api/admin/chats
POST /api/admin/chats
GET /api/admin/chats/:id
PATCH /api/admin/chats/:id
POST /api/admin/chats/:id/sync
GET /api/admin/sync-jobs
GET /api/admin/sync-jobs/:id
```

### Feishu Open Platform APIs

Use these official capabilities:

- Get chat info: useful for verifying chat id and name.
- Get message list / history messages: pull messages by chat id and time range.
- Get message content: if required by message list response shape.

References:

- [Get Message List](https://open.feishu.cn/document/server-docs/im-v1/message/list)
- [Get Message Detail](https://open.feishu.cn/document/server-docs/im-v1/message/get)
- [Chat APIs](https://open.feishu.cn/document/server-docs/im-v1/chat/summary)

### Database Tables

```sql
CREATE TABLE chats (
  id BIGSERIAL PRIMARY KEY,
  feishu_chat_id TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  chat_type TEXT,
  enabled BOOLEAN NOT NULL DEFAULT false,
  sync_enabled BOOLEAN NOT NULL DEFAULT false,
  auto_reply_enabled BOOLEAN NOT NULL DEFAULT false,
  trigger_mode TEXT NOT NULL DEFAULT 'mention_bot',
  knowledge_scope TEXT NOT NULL DEFAULT 'current_chat',
  last_synced_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sync_jobs (
  id BIGSERIAL PRIMARY KEY,
  job_type TEXT NOT NULL,
  chat_id BIGINT REFERENCES chats(id),
  status TEXT NOT NULL DEFAULT 'pending',
  cursor TEXT,
  started_at TIMESTAMPTZ,
  finished_at TIMESTAMPTZ,
  error TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

### Background Jobs

```text
sync_chat_history
  input:
    chat_id
    start_time
    end_time
    page_token

  behavior:
    fetch one page
    normalize messages
    upsert messages
    enqueue next page if needed
    update sync_jobs
```

### Admin Page: Chat Management

Fields:

- 群名称
- 飞书群 ID
- 是否启用知识库
- 是否同步历史消息
- 是否允许自动回复
- 触发方式
- 知识检索范围
- 最近同步时间
- 手动同步按钮

### Acceptance Criteria

- Admin API can create and update chat config.
- A chat can be manually synced.
- History messages are saved without duplicates.
- Sync job status can be queried.
- Failed sync jobs record error reason.

## Step 3: Knowledge Indexing and Retrieval

### Goal

把消息先聚合成上下文完整的知识单元，再转换成可检索知识片段，生成 embedding 并写入 pgvector；提供检索 API。

这一步完成后，系统已经具备“从聊天记录中找相关信息”的能力。

### Scope

- 消息清洗。
- 消息按 `chat_id + day` 或话题聚合成 `knowledge_units`。
- 知识单元再按语义和 token 长度切成 `knowledge_chunks`。
- embedding 生成。
- pgvector 写入。
- 关键词 + 向量混合检索。
- 权限范围过滤。
- 检索结果返回来源。

### Required Internal APIs

```http
POST /api/admin/chats/:id/reindex
POST /api/search
```

Request example:

```json
{
  "query": "这个客户的报价规则是什么？",
  "chat_id": 1,
  "top_k": 8
}
```

Response example:

```json
{
  "items": [
    {
      "source_type": "message",
      "source_id": "123",
      "content": "相关消息片段",
      "score": 0.82,
      "metadata": {
        "chat_name": "项目群",
        "sent_at": "2026-06-17T10:00:00Z"
      }
    }
  ]
}
```

### Database Tables

```sql
CREATE TABLE knowledge_chunks (
  id BIGSERIAL PRIMARY KEY,
  source_type TEXT NOT NULL,
  source_id TEXT NOT NULL,
  chat_id BIGINT REFERENCES chats(id),
  content TEXT NOT NULL,
  embedding VECTOR(1536),
  token_count INT,
  metadata JSONB,
  visibility_scope TEXT NOT NULL DEFAULT 'current_chat',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (source_type, source_id)
);

CREATE INDEX idx_knowledge_chunks_chat_id ON knowledge_chunks (chat_id);
CREATE INDEX idx_knowledge_chunks_embedding
  ON knowledge_chunks
  USING ivfflat (embedding vector_cosine_ops)
  WITH (lists = 100);
```

Note:

- `VECTOR(1536)` depends on the selected embedding model.
- If using `bge-m3`, dimension may be different. Keep this configurable before production.

### Chunking Strategy

MVP 不直接把单条消息作为知识。默认策略：

- 同一群按天聚合为 `knowledge_units`。
- 如果后续拿到 `thread_id`，优先按 thread 聚合。
- 单个 knowledge unit 过长时，再按 800-1200 tokens 切分为 chunks。
- 跳过纯表情、极短消息、无意义系统消息。

### Retrieval Strategy

MVP 先使用：

- pgvector cosine similarity
- chat_id / visibility filter
- recent messages slight boost in application layer

Later improvements:

- PostgreSQL full-text search
- hybrid search
- rerank model
- document source priority

### Acceptance Criteria

- Existing messages can be reindexed.
- `knowledge_chunks` contains embeddings.
- `/api/search` can return relevant chunks.
- Search result includes source metadata.
- Search respects chat scope.

## Step 4: Bot Answering

### Goal

当群里有人 `@机器人` 时，系统检索相关知识，生成答案，并通过飞书回复。

这一步完成后，MVP 闭环成立。

### Scope

- 触发判断。
- 问题改写。
- 知识检索。
- Prompt assembly。
- LLM answer generation。
- 置信度判断。
- 飞书消息回复。
- 回复日志。

### Required Internal APIs

```http
POST /api/admin/reply-logs/:id/retry
GET /api/admin/reply-logs
GET /api/admin/reply-logs/:id
```

### Feishu Open Platform APIs

Use these official capabilities:

- Reply message.
- Send message.

References:

- [Reply Message](https://open.feishu.cn/document/server-docs/im-v1/message/reply)
- [Send Message](https://open.feishu.cn/document/server-docs/im-v1/message/create)

### Database Tables

```sql
CREATE TABLE reply_logs (
  id BIGSERIAL PRIMARY KEY,
  incoming_message_id BIGINT REFERENCES messages(id),
  feishu_chat_id TEXT NOT NULL,
  query TEXT NOT NULL,
  generated_answer TEXT,
  sent_answer TEXT,
  confidence NUMERIC(5,4),
  status TEXT NOT NULL DEFAULT 'pending',
  sources JSONB,
  error TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  sent_at TIMESTAMPTZ
);
```

### Reply Policy

MVP default:

- Only reply when bot is explicitly mentioned.
- Only reply in enabled chats.
- Do not answer if no relevant chunk score is above threshold.
- Always include source summary internally.
- Optionally include source references in Feishu message.

Suggested thresholds:

- Top chunk score >= 0.72: allow reply.
- Top chunk score < 0.72: write log only, no auto reply.

### Prompt Contract

System behavior:

- Answer only from provided context.
- If context is insufficient, say cannot determine.
- Do not reveal information from other chats.
- Keep answer concise.
- Include action items when useful.

### Acceptance Criteria

- `@机器人` in enabled chat triggers pipeline.
- Disabled chat does not trigger answer.
- Low-confidence question does not auto reply.
- Successful answer is sent back to Feishu.
- Reply log records query, answer, status, confidence, and sources.

## Step 5: Admin Console MVP

### Goal

提供最小可用管理后台，让系统可配置、可观察、可调试。

### Pages

#### Dashboard

Metrics:

- 今日接收事件数
- 今日入库消息数
- 今日生成知识片段数
- 今日问答次数
- 自动回复成功数
- 低置信度拦截数
- 最近失败任务

#### Chat Management

Actions:

- 添加群
- 启用/禁用知识库
- 启用/禁用自动回复
- 设置触发模式
- 手动同步历史消息
- 手动重建索引

#### Sync Jobs

Fields:

- 任务类型
- 群
- 状态
- 开始时间
- 结束时间
- 错误信息

#### Reply Logs

Fields:

- 用户问题
- 检索来源
- 生成答案
- 是否发送
- 置信度
- 错误信息
- 重试按钮

#### Search Playground

Purpose:

- 输入问题
- 选择群
- 查看命中的知识片段
- 调试检索质量

### Acceptance Criteria

- Admin can configure enabled chats.
- Admin can trigger sync and reindex.
- Admin can inspect reply logs and source chunks.
- Admin can test retrieval manually.

## 4. MVP Build Order

Recommended implementation order:

1. Local infrastructure
2. Go server skeleton
3. Config, logging, health checks
4. PostgreSQL migrations
5. Redis connection
6. Feishu token client
7. Feishu event callback
8. Message event parsing and persistence
9. Chat config API
10. History sync job
11. Knowledge chunking
12. Embedding provider
13. Vector search API
14. Bot reply pipeline
15. Reply logs
16. Minimal admin frontend

## 5. Recommended First Three Engineering Tasks

### Task 1: Initialize Go Backend

Deliverables:

- `go.mod`
- `cmd/server/main.go`
- config package
- HTTP router
- health checks
- PostgreSQL and Redis clients

Done when:

- `go run ./cmd/server` starts.
- `/healthz` and `/readyz` work.

### Task 2: Create Core Migrations

Deliverables:

- `chats`
- `messages`
- `feishu_events`
- `sync_jobs`
- `knowledge_chunks`
- `reply_logs`

Done when:

- migrations run locally.
- pgvector extension is enabled.

### Task 3: Implement Feishu Event Callback

Deliverables:

- URL verification response.
- encrypted event decrypt support if enabled.
- message event parsing.
- event idempotency through Redis and DB unique key.
- text message persistence.

Done when:

- Feishu developer console callback verification passes.
- A real message event is stored.

## 6. Key Risks

### Feishu Permission Scope

Risk:

- The app may only receive messages from chats where the bot is present or where required permissions are granted.

Mitigation:

- MVP uses explicit enabled chats.
- Admin manually adds chat IDs.
- Avoid claiming full personal message access.

### Auto Reply Quality

Risk:

- RAG may produce incorrect or stale answers.

Mitigation:

- Default auto reply off per chat.
- Confidence threshold.
- Reply logs and source inspection.
- Start with `@机器人` only.

### Permission Leakage

Risk:

- Content from one group could be used to answer another group.

Mitigation:

- Store `chat_id` and `visibility_scope` on each chunk.
- Retrieval filters by current chat by default.
- Cross-chat retrieval must be explicit in admin config.

### Message Noise

Risk:

- Chat messages are fragmented and noisy.

Mitigation:

- Filter short/noisy messages.
- Group messages by time window.
- Later add Feishu docs as higher-quality knowledge source.

## 7. Later Iterations

After MVP:

- Feishu Docs and Wiki source sync.
- Manual FAQ editor.
- Hybrid search with full-text search.
- Rerank model.
- Human approval mode before reply.
- Personal draft replies.
- Multi-user permissions.
- Sensitive content detection.
- Observability with metrics and tracing.
