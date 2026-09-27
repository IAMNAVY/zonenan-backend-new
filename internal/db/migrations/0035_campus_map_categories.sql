-- Introduce a compact two-level taxonomy while retaining place_type for rolling
-- compatibility with older app/backend versions.
ALTER TABLE campus_map_places
  ADD COLUMN IF NOT EXISTS category VARCHAR(32),
  ADD COLUMN IF NOT EXISTS place_types TEXT[];

UPDATE campus_map_places
   SET category = CASE place_type
         WHEN 'teaching' THEN 'teaching_research'
         WHEN 'library' THEN 'teaching_research'
         WHEN 'dormitory' THEN 'life_service'
         WHEN 'dining' THEN 'life_service'
         WHEN 'service' THEN 'life_service'
         WHEN 'sports' THEN 'culture_sports'
         WHEN 'landscape' THEN 'landscape'
         ELSE 'transportation'
       END
 WHERE category IS NULL;

UPDATE campus_map_places
   SET place_types = ARRAY[
         CASE place_type
           WHEN 'teaching' THEN 'teaching_building'
           WHEN 'library' THEN 'library'
           WHEN 'dormitory' THEN 'dormitory'
           WHEN 'dining' THEN 'dining_hall'
           WHEN 'sports' THEN 'culture_sports'
           WHEN 'landscape' THEN 'landscape'
           WHEN 'parking' THEN 'parking'
           WHEN 'gate' THEN 'entrance'
           WHEN 'transport' THEN 'transportation'
           ELSE 'life_service'
         END
       ]::TEXT[]
 WHERE place_types IS NULL OR CARDINALITY(place_types) = 0;

ALTER TABLE campus_map_places
  ALTER COLUMN category SET NOT NULL,
  ALTER COLUMN place_types SET NOT NULL,
  ALTER COLUMN place_types SET DEFAULT '{}';

ALTER TABLE campus_map_places
  ADD CONSTRAINT campus_map_places_category_check CHECK (category IN (
    'teaching_research', 'transportation', 'life_service', 'culture_sports',
    'medical_health', 'government_service', 'landscape'
  )),
  ADD CONSTRAINT campus_map_places_types_check CHECK (place_types <@ ARRAY[
    'teaching_research', 'teaching_building', 'college', 'library',
    'research_experiment', 'study_space', 'transportation', 'metro',
    'campus_bus', 'shuttle_bus', 'public_bus', 'road', 'shuttle_stop',
    'shuttle_route', 'parking', 'entrance', 'life_service',
    'comprehensive_service', 'dining_hall', 'dormitory', 'restroom',
    'culture_sports', 'medical_health', 'aed', 'government_service',
    'landscape'
  ]::TEXT[]);

UPDATE campus_map_metadata
   SET revision = revision + 1,
       updated_at = NOW()
 WHERE singleton = TRUE;

