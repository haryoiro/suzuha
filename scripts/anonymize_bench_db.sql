-- ベンチ用 DB の匿名化スクリプト
-- pg_restore 後に suzuha_bench DB に対して実行する

-- チャンネル ID をハッシュ化
UPDATE conversation_logs SET channel_id = 'ch-' || md5(channel_id) WHERE channel_id != '';
UPDATE channel_activity SET channel_id = 'ch-' || md5(channel_id);
UPDATE channel_settings SET channel_id = 'ch-' || md5(channel_id);
UPDATE channel_summaries SET channel_id = 'ch-' || md5(channel_id);

-- ユーザー名を匿名化
UPDATE conversation_logs SET user_name = 'user-' || LEFT(md5(COALESCE(user_name, '')), 6)
  WHERE user_name IS NOT NULL AND user_name != '';
UPDATE users SET display_name = 'user-' || LEFT(md5(id), 6);

-- ユーザー ID をハッシュ化 (FK 整合性を保つため、users テーブルから順に)
-- ※ FK CASCADE が効くので users の id 変更は推奨しない。
-- 代わりに display_name だけ匿名化する。

-- Guild ID / Guild Name を匿名化
UPDATE guilds SET name = 'guild-' || LEFT(md5(id), 6);
UPDATE channel_settings SET guild_id = 'guild-' || md5(guild_id) WHERE guild_id != '';

-- platform_links のプラットフォーム固有 ID を匿名化
UPDATE platform_links SET
  platform_user_id = 'anon-' || LEFT(md5(platform_user_id), 8),
  platform_name = 'user-' || LEFT(md5(platform_name), 6);

-- メッセージ ID を匿名化
UPDATE conversation_logs SET message_id = NULL WHERE message_id IS NOT NULL;

-- 位置情報をぼかす (小数点以下2桁に丸め = 約1km精度)
UPDATE locations SET
  latitude = ROUND(latitude::numeric, 2)::real,
  longitude = ROUND(longitude::numeric, 2)::real,
  address = NULL,
  wifi = NULL;

-- LLM API キーを除去
UPDATE llm_providers SET api_key = '' WHERE api_key != '';

-- app_settings の機密値を除去
DELETE FROM app_settings WHERE key LIKE '%key%' OR key LIKE '%secret%';
