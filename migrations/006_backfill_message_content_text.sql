UPDATE messages
SET content_text = trim(raw_content->>'text')
WHERE message_type = 'text'
  AND nullif(trim(coalesce(content_text, '')), '') IS NULL
  AND nullif(trim(coalesce(raw_content->>'text', '')), '') IS NOT NULL;

UPDATE messages
SET content_text = CASE
  WHEN nullif(trim(coalesce(raw_content->>'image_key', '')), '') IS NULL THEN '[图片]'
  ELSE '[图片:' || (raw_content->>'image_key') || ']'
END
WHERE message_type = 'image'
  AND nullif(trim(coalesce(content_text, '')), '') IS NULL
  AND raw_content IS NOT NULL;

WITH post_lines AS (
  SELECT
    m.id,
    string_agg(line.line_text, E'\n' ORDER BY line.line_ord) AS content_text
  FROM messages m
  CROSS JOIN LATERAL jsonb_array_elements(m.raw_content->'content') WITH ORDINALITY AS block(items, line_ord)
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
  WHERE m.message_type = 'post'
    AND nullif(trim(coalesce(m.content_text, '')), '') IS NULL
    AND jsonb_typeof(m.raw_content->'content') = 'array'
  GROUP BY m.id
)
UPDATE messages m
SET content_text = nullif(trim(post_lines.content_text), '')
FROM post_lines
WHERE m.id = post_lines.id
  AND nullif(trim(post_lines.content_text), '') IS NOT NULL;
