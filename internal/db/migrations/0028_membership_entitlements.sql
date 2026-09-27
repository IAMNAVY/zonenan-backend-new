-- Move the legacy beta/content entitlement into the real membership model
-- without deleting the legacy table used by older clients and admin tooling.
INSERT INTO membership_grants(user_id, membership_type_key, source, external_ref, expires_at, metadata)
SELECT b.user_id, 'premium', 'legacy', 'legacy-beta:' || b.user_id,
       TIMESTAMPTZ '2099-12-31 23:59:59+08',
       '{"migrated_from":"beta_memberships"}'::jsonb
  FROM beta_memberships b
 WHERE NOT EXISTS (
   SELECT 1 FROM membership_grants g
    WHERE g.user_id=b.user_id
      AND g.membership_type_key='premium'
      AND g.source='legacy'
      AND g.external_ref='legacy-beta:' || b.user_id
 );

UPDATE membership_types
   SET features = features || '[{"key":"course_share_hide_brand","title":"课程分享去除品牌尾部","description":"分享课程表时隐藏 ZoneNaN 品牌尾部"}]'::jsonb,
       updated_at = NOW()
 WHERE type_key='premium'
   AND NOT EXISTS (
     SELECT 1
       FROM jsonb_array_elements(features) f
      WHERE f->>'key'='course_share_hide_brand'
   );

