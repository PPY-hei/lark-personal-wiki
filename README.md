# Feishu KB Assistant

飞书知识库助理，用 Go + PostgreSQL/pgvector + Redis 构建。

## Local Infrastructure

启动 PostgreSQL 和 Redis：

```bash
docker compose up -d
```

服务版本：

- PostgreSQL: `docker.m.daocloud.io/pgvector/pgvector:pg16`
- Redis: `docker.m.daocloud.io/library/redis:7.2-alpine`

默认连接：

- PostgreSQL: `postgres://feishu_kb:feishu_kb_password@localhost:5432/feishu_kb?sslmode=disable`
- Redis: `localhost:6379`

停止服务：

```bash
docker compose down
```

## Backend

启动 Go 服务：

```bash
go run ./cmd/server
```

默认本地地址：

```text
http://localhost:8081
```

事件接收默认使用飞书长连接：

```text
FEISHU_EVENT_MODE=websocket
```

因此本地开发不需要公网 IP 或内网穿透。

更多飞书配置步骤见：

- [docs/feishu-setup.md](docs/feishu-setup.md)
- [docs/mvp-roadmap.md](docs/mvp-roadmap.md)
- [docs/knowledge-unit-strategy.md](docs/knowledge-unit-strategy.md)
