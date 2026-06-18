# Knowledge Unit Strategy

聊天记录不能按单条消息直接进入知识库。单条消息经常只是代码片段、短回复、确认语或上下文中的一环，直接 embedding 会产生大量低质量知识。

MVP 采用“知识单元”策略：

## 1. Default Unit: Chat + Day

默认按下面维度聚合：

```text
chat_id + local_date
```

例如：

```text
oc_xxx / 2026-06-18
```

一天内同一个群的消息会按时间顺序拼成一个知识单元，保留：

- 发送人
- 发送时间
- 消息类型
- 原始文本
- message_id

适合：

- 项目群日报式讨论
- 需求沟通
- 问题排查过程
- 决策上下文

## 2. Future Unit: Thread / Topic / Time Window

后续可升级为：

```text
chat_id + thread_id
chat_id + topic_group + day
chat_id + rolling_time_window
```

建议优先级：

1. 如果消息有 `thread_id`，优先按 thread 聚合。
2. 如果是普通群，按天聚合。
3. 如果一天消息过多，再按 2-4 小时窗口切分。

## 3. Chunking

知识单元可能很长，生成 embedding 前再切 chunk：

```text
knowledge_units
  -> normalized daily transcript
  -> semantic chunks
  -> knowledge_chunks with embedding
```

切分规则：

- 保持时间顺序。
- 不打断代码块。
- 每个 chunk 约 800-1200 tokens。
- chunk metadata 记录对应 message_id 范围。

## 4. Prompt Retrieval

检索时不直接展示单条消息，而是展示：

- 命中的 chunk
- 所属 knowledge_unit
- 原始群
- 日期
- 涉及 message_id 范围

这样回答时可以引用“某群某天的讨论”，而不是孤立引用一句话。

## 5. Database

当前已预留：

```text
knowledge_units
```

后续索引流程：

```text
messages -> knowledge_units -> knowledge_chunks -> RAG answer
```
