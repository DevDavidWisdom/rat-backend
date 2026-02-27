-- Geofences table
CREATE TABLE IF NOT EXISTS geofences (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    organization_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    latitude DECIMAL(10, 8) NOT NULL,
    longitude DECIMAL(11, 8) NOT NULL,
    radius FLOAT NOT NULL DEFAULT 500,
    action VARCHAR(50) NOT NULL DEFAULT 'NOTIFY',
    group_id UUID REFERENCES device_groups(id) ON DELETE SET NULL,
    enrollment_id UUID REFERENCES enrollment_tokens(id) ON DELETE SET NULL,
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Geofence breaches log
CREATE TABLE IF NOT EXISTS geofence_breaches (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    geofence_id UUID REFERENCES geofences(id) ON DELETE CASCADE,
    device_id UUID REFERENCES devices(id) ON DELETE CASCADE,
    device_latitude DECIMAL(10, 8),
    device_longitude DECIMAL(11, 8),
    distance_meters FLOAT,
    resolved BOOLEAN DEFAULT false,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_geofences_org ON geofences(organization_id);
CREATE INDEX IF NOT EXISTS idx_geofences_group ON geofences(group_id);
CREATE INDEX IF NOT EXISTS idx_geofence_breaches_geofence ON geofence_breaches(geofence_id);
CREATE INDEX IF NOT EXISTS idx_geofence_breaches_device ON geofence_breaches(device_id);
CREATE INDEX IF NOT EXISTS idx_geofence_breaches_created ON geofence_breaches(created_at DESC);
