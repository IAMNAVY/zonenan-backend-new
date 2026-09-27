-- Widget activity dimensions and event types for PAU reporting.
ALTER TABLE analytics_events
  ADD COLUMN IF NOT EXISTS widget_kind TEXT,
  ADD COLUMN IF NOT EXISTS widget_size TEXT,
  ADD COLUMN IF NOT EXISTS widget_refresh_mode TEXT,
  ADD COLUMN IF NOT EXISTS widget_action TEXT;

ALTER TABLE analytics_events
  DROP CONSTRAINT IF EXISTS analytics_events_event_type_check,
  DROP CONSTRAINT IF EXISTS analytics_events_check;

ALTER TABLE analytics_events
  ADD CONSTRAINT analytics_events_event_type_check CHECK (event_type IN (
    'session_start', 'screen_click', 'screen_view', 'screen_duration',
    'feature_open', 'feature_result', 'ad_impression', 'ad_click',
    'widget_enabled', 'widget_disabled', 'widget_render', 'widget_click',
    'widget_click_open_app', 'widget_refresh', 'widget_refresh_failed'
  )),
  ADD CONSTRAINT analytics_events_widget_kind_check CHECK (
    widget_kind IS NULL OR widget_kind IN ('today', 'weekly')
  ),
  ADD CONSTRAINT analytics_events_widget_size_check CHECK (
    widget_size IS NULL OR widget_size IN ('small', 'medium', 'large')
  ),
  ADD CONSTRAINT analytics_events_widget_refresh_mode_check CHECK (
    widget_refresh_mode IS NULL OR widget_refresh_mode IN ('auto', 'manual')
  ),
  ADD CONSTRAINT analytics_events_widget_action_check CHECK (
    widget_action IS NULL OR widget_action IN ('open_app', 'refresh', 'previous', 'next', 'today', 'content')
  ),
  ADD CONSTRAINT analytics_events_widget_shape_check CHECK (
    (event_type NOT LIKE 'widget_%') OR
    (widget_kind IS NOT NULL AND widget_size IS NOT NULL AND widget_refresh_mode IS NOT NULL AND
      ((event_type IN ('widget_click', 'widget_click_open_app') AND widget_action IS NOT NULL) OR
       (event_type NOT IN ('widget_click', 'widget_click_open_app') AND widget_action IS NULL)))
  );

CREATE INDEX IF NOT EXISTS idx_analytics_events_widget_time
  ON analytics_events(widget_kind, widget_size, occurred_at DESC)
  WHERE widget_kind IS NOT NULL;

