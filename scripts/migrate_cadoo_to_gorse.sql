-- Migration script: Cadoo user data → Gorse users/feedback/items tables
-- Run: psql -f migrate_cadoo_to_gorse.sql
-- Target: postgres://tajian:Postgres@1234@pyramidtip.pg.polardb.rds.aliyuncs.com:5432/recommend

BEGIN;

-- ============================================================
-- Step 1: Migrate users from user_profiles → users
-- ============================================================

INSERT INTO users (user_id, gender, labels, comment)
SELECT
    up.user_id,
    -- Normalize gender: female/FEMALE → F, male/MALE → M, otherwise O
    CASE
        WHEN LOWER(COALESCE(up.gender, '')) IN ('female') THEN 'F'
        WHEN LOWER(COALESCE(up.gender, '')) IN ('male')   THEN 'M'
        ELSE 'O'
    END AS gender,
    -- Build labels JSON with enriched profile data
    jsonb_build_object(
        'age',              up.age,
        'nickname',         up.nickname,
        'height',          up.height,
        'weight',          up.weight,
        'hobbies',         up.hobbies,
        'profession',       up.profession,
        'education_level',  up.education_level,
        'dating_type',      up.dating_type,
        'personalities',    up.personalities,
        'birthday',        up.birthday,
        'constellation',   up.constellation,
        'income_level',    up.income_level,
        'introduction',     left(up.introduction, 500)
    )::json AS labels,
    COALESCE(up.nickname, up.user_id) AS comment
FROM user_profiles up
ON CONFLICT (user_id) DO NOTHING;

-- ============================================================
-- Step 2: Migrate likes → feedback (feedback_type = 'like')
-- ============================================================

INSERT INTO feedback (feedback_type, user_id, item_id, value, time_stamp, updated, comment)
SELECT
    'like' AS feedback_type,
    uil.user_id,
    uil.target_user_id AS item_id,
    1.0 AS value,
    uil.create_time AS time_stamp,
    uil.create_time AS updated,
    ''::text AS comment
FROM user_interaction_like uil
WHERE uil.user_id IS NOT NULL
  AND uil.target_user_id IS NOT NULL
ON CONFLICT (feedback_type, user_id, item_id) DO NOTHING;

-- ============================================================
-- Step 3: Migrate dislikes → feedback (feedback_type = 'dislike')
-- ============================================================

INSERT INTO feedback (feedback_type, user_id, item_id, value, time_stamp, updated, comment)
SELECT
    'dislike' AS feedback_type,
    uid.user_id,
    uid.target_user_id AS item_id,
    1.0 AS value,
    uid.create_time AS time_stamp,
    uid.create_time AS updated,
    ''::text AS comment
FROM user_interaction_dislike uid
WHERE uid.user_id IS NOT NULL
  AND uid.target_user_id IS NOT NULL
ON CONFLICT (feedback_type, user_id, item_id) DO NOTHING;

-- ============================================================
-- Step 4: Create items for all users (dating app: users = items)
-- In a dating app, items table = all users for collaborative filtering
-- Items table: user_id → item_id, profile data → item metadata
-- ============================================================

INSERT INTO items (item_id, is_hidden, categories, time_stamp, labels, comment)
SELECT
    u.user_id AS item_id,
    false AS is_hidden,
    -- Categories from gender (JSON array)
    CASE
        WHEN u.gender = 'F' THEN '["F"]'::json
        WHEN u.gender = 'M' THEN '["M"]'::json
        ELSE '["O"]'::json
    END AS categories,
    NOW() AS time_stamp,
    u.labels AS labels,
    COALESCE(u.comment, u.user_id) AS comment
FROM users u
WHERE u.user_id NOT LIKE 'MOCK%'
ON CONFLICT (item_id) DO NOTHING;

-- ============================================================
-- Step 5: Summary
-- ============================================================

DO $$
BEGIN
  RAISE NOTICE '=== Migration Complete ===';
  RAISE NOTICE 'Users inserted:    %', (SELECT COUNT(*) FROM users);
  RAISE NOTICE 'Items inserted:    %', (SELECT COUNT(*) FROM items);
  RAISE NOTICE 'Feedback inserted: %', (SELECT COUNT(*) FROM feedback);
END $$;

COMMIT;
