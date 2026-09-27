-- Keep database allowlists aligned with the HTTP analytics protocol. The app
-- already emits these normalized names; stale CHECK constraints rejected them
-- at insert time and prevented the admin summary from seeing the events.
ALTER TABLE analytics_events
  DROP CONSTRAINT IF EXISTS analytics_events_screen_check,
  DROP CONSTRAINT IF EXISTS analytics_events_feature_check;

ALTER TABLE analytics_events
  ADD CONSTRAINT analytics_events_screen_check CHECK (screen IS NULL OR screen IN (
    'timetable', 'status', 'functions', 'news', 'campus_card', 'library',
    'grades', 'grade_rating', 'schedule', 'shuttle', 'classroom',
    'academic_calendar', 'campus_map'
  )),
  ADD CONSTRAINT analytics_events_feature_check CHECK (feature IS NULL OR feature IN (
    'timetable', 'status', 'functions', 'news', 'campus_card', 'library',
    'grades', 'grade_rating', 'schedule', 'shuttle', 'classroom',
    'academic_calendar', 'campus_map'
  ));

