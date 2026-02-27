-- Add enrollment_id to attendance_zones (mirrors geofences pattern)
ALTER TABLE attendance_zones
  ADD COLUMN IF NOT EXISTS enrollment_id UUID REFERENCES enrollment_tokens(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_attendance_zones_enrollment ON attendance_zones(enrollment_id);
