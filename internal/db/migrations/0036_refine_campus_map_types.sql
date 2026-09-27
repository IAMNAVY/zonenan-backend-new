-- Refine the controlled secondary-place taxonomy without requiring existing
-- campus-map records to be edited manually.
ALTER TABLE campus_map_places
  DROP CONSTRAINT IF EXISTS campus_map_places_types_check;

UPDATE campus_map_places
   SET place_types = ARRAY(
     SELECT DISTINCT CASE value
       WHEN 'road' THEN 'transportation'
       WHEN 'entrance' THEN 'transportation'
       WHEN 'shuttle_stop' THEN 'campus_bus'
       WHEN 'shuttle_route' THEN 'campus_bus'
       WHEN 'culture_sports' THEN 'sports_venue'
       ELSE value
     END
       FROM UNNEST(place_types) AS old_type(value)
   )::TEXT[];

ALTER TABLE campus_map_places
  ADD CONSTRAINT campus_map_places_types_check CHECK (place_types <@ ARRAY[
    'teaching_research', 'teaching_building', 'college', 'library',
    'research_experiment', 'study_space', 'transportation', 'metro',
    'public_bus', 'shuttle_bus', 'campus_bus', 'parking', 'life_service',
    'comprehensive_service', 'dining_hall', 'dormitory', 'restroom',
    'supermarket', 'express', 'merchant', 'sports_venue', 'cultural_venue',
    'medical_health', 'campus_hospital', 'medical_station', 'aed',
    'government_service', 'landscape'
  ]::TEXT[]);

UPDATE campus_map_metadata
   SET revision = revision + 1,
       updated_at = NOW()
 WHERE singleton = TRUE;

