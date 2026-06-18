WITH RECURSIVE mention_pairs AS (
  SELECT
    m.id,
    mention.ord::int AS ord,
    mention.value->>'key' AS mention_key,
    '@' || (mention.value->>'name') AS mention_name
  FROM messages m
  CROSS JOIN LATERAL jsonb_array_elements(
    CASE
      WHEN jsonb_typeof(m.raw_payload->'mentions') = 'array' THEN m.raw_payload->'mentions'
      WHEN jsonb_typeof(m.raw_payload->'message'->'mentions') = 'array' THEN m.raw_payload->'message'->'mentions'
      ELSE '[]'::jsonb
    END
  ) WITH ORDINALITY AS mention(value, ord)
  WHERE m.content_text ~ '@_user_[0-9]+'
    AND nullif(trim(coalesce(mention.value->>'key', '')), '') IS NOT NULL
    AND nullif(trim(coalesce(mention.value->>'name', '')), '') IS NOT NULL
),
mention_steps AS (
  SELECT
    m.id,
    0 AS ord,
    m.content_text
  FROM messages m
  WHERE m.content_text ~ '@_user_[0-9]+'

  UNION ALL

  SELECT
    mention_steps.id,
    mention_pairs.ord,
    replace(mention_steps.content_text, mention_pairs.mention_key, mention_pairs.mention_name)
  FROM mention_steps
  JOIN mention_pairs
    ON mention_pairs.id = mention_steps.id
   AND mention_pairs.ord = mention_steps.ord + 1
),
rewritten AS (
  SELECT DISTINCT ON (id)
    id,
    content_text
  FROM mention_steps
  ORDER BY id, ord DESC
)
UPDATE messages m
SET content_text = rewritten.content_text
FROM rewritten
WHERE m.id = rewritten.id
  AND rewritten.content_text <> m.content_text;
