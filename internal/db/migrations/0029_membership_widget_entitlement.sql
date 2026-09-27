-- Extend Premium entitlements for the Android weekly timetable widget and
-- retain grant-expiry edits as an auditable administration action.
ALTER TABLE membership_audit_logs
  DROP CONSTRAINT IF EXISTS membership_audit_logs_action_check;
ALTER TABLE membership_audit_logs
  ADD CONSTRAINT membership_audit_logs_action_check
  CHECK (action IN ('grant', 'revoke', 'generate_code', 'disable_code', 'sync', 'update_grant'));

UPDATE membership_types
   SET features = features || '[{"key":"timetable_full_semester","title":"周课表小组件查看整学期","description":"在周课表小组件中查看并切换整学期周次"}]'::jsonb,
       updated_at = NOW()
 WHERE type_key='premium'
   AND NOT EXISTS (
     SELECT 1
       FROM jsonb_array_elements(CASE WHEN jsonb_typeof(features)='array' THEN features ELSE '[]'::jsonb END) f
      WHERE f->>'key'='timetable_full_semester'
   );

