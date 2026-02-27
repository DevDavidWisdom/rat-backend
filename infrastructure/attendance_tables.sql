-- Attendance zones (walk-calibrated polygons)
CREATE TABLE IF NOT EXISTS attendance_zones (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    organization_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    group_id UUID REFERENCES device_groups(id) ON DELETE SET NULL,
    enrollment_id UUID REFERENCES enrollment_tokens(id) ON DELETE SET NULL,
    name VARCHAR(255) NOT NULL,
    polygon JSONB NOT NULL,
    buffered_polygon JSONB NOT NULL,
    buffer_meters INTEGER DEFAULT 30,
    center_lat DECIMAL(10, 8),
    center_lng DECIMAL(11, 8),
    is_active BOOLEAN DEFAULT true,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Attendance sessions (each take attendance run)
CREATE TABLE IF NOT EXISTS attendance_sessions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    zone_id UUID REFERENCES attendance_zones(id) ON DELETE CASCADE,
    organization_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    initiated_by UUID REFERENCES users(id) ON DELETE SET NULL,
    status VARCHAR(50) DEFAULT 'in_progress',
    total_devices INTEGER DEFAULT 0,
    present_count INTEGER DEFAULT 0,
    absent_count INTEGER DEFAULT 0,
    offline_count INTEGER DEFAULT 0,
    uncertain_count INTEGER DEFAULT 0,
    timeout_seconds INTEGER DEFAULT 30,
    initiated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    completed_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Individual attendance records per device per session
CREATE TABLE IF NOT EXISTS attendance_records (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    session_id UUID REFERENCES attendance_sessions(id) ON DELETE CASCADE,
    device_id UUID REFERENCES devices(id) ON DELETE CASCADE,
    status VARCHAR(50) DEFAULT 'pending',
    latitude DECIMAL(10, 8),
    longitude DECIMAL(11, 8),
    gps_accuracy DECIMAL(8, 2),
    wifi_scan JSONB,
    battery_level INTEGER,
    connection_type VARCHAR(50),
    response_time_ms INTEGER,
    responded_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(session_id, device_id)
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_attendance_zones_org ON attendance_zones(organization_id);
CREATE INDEX IF NOT EXISTS idx_attendance_zones_group ON attendance_zones(group_id);
CREATE INDEX IF NOT EXISTS idx_attendance_sessions_zone ON attendance_sessions(zone_id);
CREATE INDEX IF NOT EXISTS idx_attendance_sessions_status ON attendance_sessions(status);
CREATE INDEX IF NOT EXISTS idx_attendance_records_session ON attendance_records(session_id);
CREATE INDEX IF NOT EXISTS idx_attendance_records_device ON attendance_records(device_id);
CREATE INDEX IF NOT EXISTS idx_attendance_records_status ON attendance_records(status);

-- Trigger for updated_at on zones
DROP TRIGGER IF EXISTS update_attendance_zones_updated_at ON attendance_zones;
CREATE TRIGGER update_attendance_zones_updated_at BEFORE UPDATE ON attendance_zones FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
