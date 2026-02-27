-- Convert geofences from circle (center+radius) to polygon (array of points)
ALTER TABLE geofences ADD COLUMN IF NOT EXISTS polygon JSONB;
ALTER TABLE geofences DROP COLUMN IF EXISTS latitude;
ALTER TABLE geofences DROP COLUMN IF EXISTS longitude;
ALTER TABLE geofences DROP COLUMN IF EXISTS radius;
