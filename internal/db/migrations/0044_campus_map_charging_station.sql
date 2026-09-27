-- Restore charging facilities as a life-service subtype. The legacy `type`
-- column remains `service` for compatibility with older app versions.
ALTER TABLE campus_map_places
  DROP CONSTRAINT IF EXISTS campus_map_places_types_check;

ALTER TABLE campus_map_places
  ADD CONSTRAINT campus_map_places_types_check CHECK (place_types <@ ARRAY[
    'teaching_research', 'teaching_building', 'college', 'library',
    'research_experiment', 'study_space', 'transportation', 'metro',
    'public_bus', 'shuttle_bus', 'campus_bus', 'parking', 'entrance',
    'life_service', 'comprehensive_service', 'dining_hall', 'dormitory',
    'restroom', 'supermarket', 'express', 'merchant', 'charging_station',
    'printing_service', 'sports_venue', 'cultural_venue', 'medical_health', 'campus_hospital',
    'medical_station', 'aed', 'government_service', 'landscape'
  ]::TEXT[]);

UPDATE campus_map_metadata
   SET revision = revision + 1,
       updated_at = NOW()
 WHERE singleton = TRUE;
