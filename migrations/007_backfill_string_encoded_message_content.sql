WITH normalized AS (
  SELECT
    id,
    CASE
      WHEN jsonb_typeof(raw_content) = 'string' AND left(trim(raw_content #>> '{}'), 1) = '{' THEN (raw_content #>> '{}')::jsonb
      ELSE raw_content
    END AS content
  FROM messages
  WHERE message_type = 'text'
    AND raw_content IS NOT NULL
    AND nullif(trim(coalesce(content_text, '')), '') IS NULL
)
UPDATE messages m
SET content_text = trim(normalized.content->>'text')
FROM normalized
WHERE m.id = normalized.id
  AND nullif(trim(coalesce(normalized.content->>'text', '')), '') IS NOT NULL;

WITH normalized AS (
  SELECT
    id,
    CASE
      WHEN jsonb_typeof(raw_content) = 'string' AND left(trim(raw_content #>> '{}'), 1) = '{' THEN (raw_content #>> '{}')::jsonb
      ELSE raw_content
    END AS content
  FROM messages
  WHERE message_type = 'image'
    AND raw_content IS NOT NULL
    AND nullif(trim(coalesce(content_text, '')), '') IS NULL
)
UPDATE messages m
SET content_text = CASE
  WHEN nullif(trim(coalesce(normalized.content->>'image_key', '')), '') IS NULL THEN '[图片]'
  ELSE '[图片:' || (normalized.content->>'image_key') || ']'
END
FROM normalized
WHERE m.id = normalized.id;

WITH normalized AS (
  SELECT
    id,
    CASE
      WHEN jsonb_typeof(raw_content) = 'string' AND left(trim(raw_content #>> '{}'), 1) = '{' THEN (raw_content #>> '{}')::jsonb
      ELSE raw_content
    END AS content
  FROM messages
  WHERE message_type = 'post'
    AND raw_content IS NOT NULL
    AND nullif(trim(coalesce(content_text, '')), '') IS NULL
),
post_lines AS (
  SELECT
    normalized.id,
    string_agg(line.line_text, E'\n' ORDER BY line.line_ord) AS content_text
  FROM normalized
  CROSS JOIN LATERAL jsonb_array_elements(normalized.content->'content') WITH ORDINALITY AS block(items, line_ord)
  CROSS JOIN LATERAL (
    SELECT
      block.line_ord,
      string_agg(
        trim(CASE item.value->>'tag'
          WHEN 'text' THEN coalesce(item.value->>'text', '')
          WHEN 'at' THEN '@' || coalesce(nullif(item.value->>'user_name', ''), nullif(item.value->>'user_id', ''), '')
          WHEN 'a' THEN trim(coalesce(item.value->>'text', '') || ' ' || coalesce(item.value->>'href', ''))
          WHEN 'img' THEN CASE
            WHEN nullif(item.value->>'image_key', '') IS NULL THEN '[图片]'
            ELSE '[图片:' || (item.value->>'image_key') || ']'
          END
          WHEN 'media' THEN CASE
            WHEN nullif(item.value->>'file_key', '') IS NULL THEN '[视频]'
            ELSE '[视频:' || (item.value->>'file_key') || ']'
          END
          WHEN 'file' THEN CASE
            WHEN nullif(item.value->>'file_name', '') IS NOT NULL THEN '[文件:' || (item.value->>'file_name') || ']'
            WHEN nullif(item.value->>'file_key', '') IS NOT NULL THEN '[文件:' || (item.value->>'file_key') || ']'
            ELSE '[文件]'
          END
          ELSE coalesce(item.value->>'text', '')
        END),
        ' ' ORDER BY item.item_ord
      ) AS line_text
    FROM jsonb_array_elements(block.items) WITH ORDINALITY AS item(value, item_ord)
  ) line
  WHERE jsonb_typeof(normalized.content->'content') = 'array'
  GROUP BY normalized.id
)
UPDATE messages m
SET content_text = nullif(trim(post_lines.content_text), '')
FROM post_lines
WHERE m.id = post_lines.id
  AND nullif(trim(post_lines.content_text), '') IS NOT NULL;
