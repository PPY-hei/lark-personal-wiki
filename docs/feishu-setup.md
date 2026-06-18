# Feishu App Setup Guide

本文档记录 MVP 阶段需要在飞书开放平台完成的配置。

## 1. Local Service

当前后端服务默认运行在：

```text
http://localhost:8081
```

本地启动：

```bash
cd /Users/chy/Documents/report/feishu-kb-assistant
docker compose up -d
go run ./cmd/server
```

健康检查：

```bash
curl http://localhost:8081/healthz
curl http://localhost:8081/readyz
```

飞书 token 检查：

```bash
curl http://localhost:8081/api/admin/feishu/token
```

当前 MVP 默认使用飞书“长连接”接收事件，不需要 ngrok、frp、公网 IP 或部署服务器。

## 2. Current App Config

本地 `.env` 已配置：

```text
FEISHU_APP_ID=cli_a9257fd046bbdcce
FEISHU_APP_SECRET=<stored in local .env>
FEISHU_EVENT_MODE=websocket
FEISHU_OAUTH_REDIRECT_URI=http://localhost:8081/api/auth/feishu/callback
```

注意：

- `.env` 已加入 `.gitignore`，不要提交到仓库。
- 如果飞书后台启用了 Verification Token 或 Encrypt Key，需要同步填入 `.env`。

## 3. Required Feishu Permissions

MVP 阶段建议申请这些权限。不同租户后台显示名称可能略有差异，以开放平台为准。

### User Authorization

后台选择群组/联系人需要飞书用户授权：

```text
authen
```

在飞书应用后台配置 OAuth 重定向 URL：

```text
http://localhost:8081/api/auth/feishu/callback
```

如果飞书后台要求 HTTPS 或可信域名，本地开发可临时使用 ngrok/cloudflared 暴露 OAuth callback。事件接收仍然走长连接，不需要公网。

### Event Receiving

用于接收群里发给机器人的消息：

```text
im.message.receive_v1
```

### Message APIs

后续发送/回复消息需要：

```text
im:message
im:message:send_as_bot
```

### History Sync

后续拉取历史消息需要：

```text
im:message:readonly
im:chat:readonly
```

### User / Chat Metadata

用于识别用户、群名称、群信息：

```text
contact:user.base:readonly
im:chat
im:chat:readonly
```

拉取“当前授权用户所在群列表”需要群列表相关权限。通讯录联系人通常还需要通讯录权限、部门范围授权或管理员审批。

如果权限申请页面没有完全相同的名称，优先搜索：

- message
- chat
- bot
- contact
- readonly

## 4. Event Subscription Configuration: Long Connection

进入飞书开放平台：

```text
开发者后台 -> 你的应用 -> 事件订阅
```

### Subscription Method

选择：

```text
使用长连接接收事件
```

使用长连接后，飞书开放平台会通过 SDK 建立的 WebSocket 通道把事件推到本地服务。你的本机只需要能访问公网，不需要被公网访问。

### Subscribe Events

订阅事件：

```text
接收消息 v2.0 / im.message.receive_v1
```

注意：

- 需要先启用机器人能力。
- 需要把应用安装到当前租户。
- 需要把机器人拉入测试群。
- 如果只有“获取用户在群组中 @ 机器人的消息”权限，那么群里必须 `@机器人` 才能收到事件。
- 如果申请到“获取群组中所有消息”权限，机器人所在群的普通消息也可以收到，具体以飞书审核通过的权限为准。

### Verification Token

长连接模式下，SDK 会负责连接和事件分发；当前服务仍然保留 Verification Token 配置，用于 HTTP webhook 备用模式。

MVP 阶段可以先留空：

```text
FEISHU_VERIFICATION_TOKEN=
```

如果后续切回 HTTP webhook，可在飞书后台填写 token，并把同样的值写入 `.env`。

### Encrypt Key

长连接模式优先依赖官方 SDK 处理事件传输；MVP 第一轮建议不配置 Encrypt Key。

如果后续使用 HTTP webhook 并开启加密，需要写入：

```text
FEISHU_ENCRYPT_KEY=你的_encrypt_key
```

## 5. Optional: HTTP Webhook Mode

如果后续要切回 HTTP webhook，把 `.env` 改成：

```text
FEISHU_EVENT_MODE=http
```

或者同时开启 HTTP 和长连接：

```text
FEISHU_EVENT_MODE=both
```

HTTP webhook 模式需要把 `localhost:8081` 暴露成公网 HTTPS 地址。飞书后台 Request URL 填：

```text
https://你的公网域名/api/feishu/events
```

HTTP webhook 的 URL verification 会返回：

```json
{
  "challenge": "飞书传入的 challenge"
}
```

## 6. Bot Configuration

进入：

```text
开发者后台 -> 应用能力 -> 机器人
```

需要：

- 启用机器人能力。
- 发布或安装应用到当前租户。
- 把机器人拉进要测试的群。
- 在群里 `@机器人` 发送一条文本消息。

当前代码会先把消息落库；自动回复将在下一阶段实现。

## 7. Admin APIs Available Now

后台页面：

```text
http://localhost:8081/admin
```

页面能力：

- 飞书用户授权
- 拉取当前授权用户可见群组
- 勾选群组并写入 `chats`
- 按部门 ID 拉取联系人
- 勾选联系人并写入 `contacts`

### Health

```http
GET /healthz
GET /readyz
```

### Feishu Token Test

```http
GET /api/admin/feishu/token
```

### Chat Config

```http
GET /api/admin/chats
POST /api/admin/chats
```

### User Authorization

```http
GET /api/auth/feishu/login
GET /api/auth/feishu/callback
GET /api/admin/me
```

### Source Selection

```http
GET /api/admin/source/chats
POST /api/admin/source/chats/select
GET /api/admin/source/contacts?department_id=xxx
POST /api/admin/source/contacts/select
```

Create or update chat:

```bash
curl -X POST http://localhost:8081/api/admin/chats \
  -H 'Content-Type: application/json' \
  -d '{
    "feishu_chat_id": "oc_xxx",
    "name": "测试群",
    "chat_type": "group",
    "enabled": true,
    "sync_enabled": true,
    "auto_reply_enabled": false,
    "trigger_mode": "mention_bot",
    "knowledge_scope": "current_chat"
  }'
```

### Message Stats

```http
GET /api/admin/messages/stats
```

## 8. Local Callback Simulation

下面的模拟请求只测试 HTTP handler。即使 MVP 使用长连接，这些接口仍可用于本地验证事件入库逻辑。

URL verification:

```bash
curl -X POST http://localhost:8081/api/feishu/events \
  -H 'Content-Type: application/json' \
  -d '{"type":"url_verification","token":"","challenge":"test_challenge"}'
```

Message event:

```bash
curl -X POST http://localhost:8081/api/feishu/events \
  -H 'Content-Type: application/json' \
  -d '{
    "schema": "2.0",
    "header": {
      "event_id": "evt_local_001",
      "event_type": "im.message.receive_v1",
      "create_time": "1781692000000",
      "token": "",
      "app_id": "cli_a9257fd046bbdcce",
      "tenant_key": "tenant"
    },
    "event": {
      "sender": {
        "sender_id": {
          "open_id": "ou_test_user"
        }
      },
      "message": {
        "message_id": "om_local_001",
        "chat_id": "oc_test_chat",
        "message_type": "text",
        "content": {
          "text": "hello from local test"
        },
        "create_time": "1781692000000"
      }
    }
  }'
```

查看结果：

```bash
curl http://localhost:8081/api/admin/messages/stats
```

## 9. How to Verify Long Connection

1. 启动基础服务：

```bash
docker compose up -d
go run ./cmd/server
```

2. 看到类似日志：

```text
starting feishu websocket event receiver
```

3. 在飞书开放平台确认：

```text
事件订阅 -> 使用长连接接收事件
事件列表 -> 接收消息 v2.0
```

4. 把机器人拉进测试群。

5. 在群里发送：

```text
@机器人 hello
```

6. 查看消息统计：

```bash
curl http://localhost:8081/api/admin/messages/stats
```

7. 或直接查数据库：

```bash
docker exec feishu-kb-postgres psql -U feishu_kb -d feishu_kb \
  -c "SELECT feishu_message_id, feishu_chat_id, message_type, content_text, created_at FROM messages ORDER BY id DESC LIMIT 5;"
```

## 10. Next Development Step

下一步建议实现：

1. 飞书真实消息事件联调。
2. 群配置和消息事件关联，只保存启用群或至少标记群状态。
3. 历史消息同步接口。
4. 消息切片与 embedding 入库。
5. `@机器人` 后的检索与回复。
