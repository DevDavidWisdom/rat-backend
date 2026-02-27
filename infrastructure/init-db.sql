-- MDM System Database Initialization Script
-- This runs automatically when PostgreSQL container starts

-- Enable UUID extension
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- Create enum types
CREATE TYPE device_status AS ENUM ('online', 'offline', 'pending', 'disabled');
CREATE TYPE command_status AS ENUM ('pending', 'queued', 'delivered', 'executing', 'completed', 'failed', 'timeout');
CREATE TYPE user_role AS ENUM ('super_admin', 'admin', 'operator', 'viewer');

-- Organizations table (multi-tenancy ready)
CREATE TABLE organizations (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(255) NOT NULL,
    slug VARCHAR(100) UNIQUE NOT NULL,
    settings JSONB DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Users table (admin users)
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    organization_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    email VARCHAR(255) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL,
    role user_role DEFAULT 'viewer',
    is_active BOOLEAN DEFAULT true,
    last_login TIMESTAMP WITH TIME ZONE,
    two_factor_secret VARCHAR(255),
    two_factor_enabled BOOLEAN DEFAULT false,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Device groups table
CREATE TABLE device_groups (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    organization_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    parent_group_id UUID REFERENCES device_groups(id) ON DELETE SET NULL,
    settings JSONB DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Policies table
CREATE TABLE policies (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    organization_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    rules JSONB NOT NULL DEFAULT '{}',
    is_default BOOLEAN DEFAULT false,
    priority INTEGER DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Devices table
CREATE TABLE devices (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    organization_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    group_id UUID REFERENCES device_groups(id) ON DELETE SET NULL,
    policy_id UUID REFERENCES policies(id) ON DELETE SET NULL,
    
    -- Identification
    serial_number VARCHAR(255),
    device_id VARCHAR(255) UNIQUE NOT NULL,  -- Android ID or custom
    enrollment_token VARCHAR(255),
    device_token VARCHAR(512),  -- For authentication
    
    -- Device info
    name VARCHAR(255),
    model VARCHAR(255),
    manufacturer VARCHAR(255),
    android_version VARCHAR(50),
    sdk_version INTEGER,
    agent_version VARCHAR(50),
    
    -- Status
    status device_status DEFAULT 'pending',
    last_seen TIMESTAMP WITH TIME ZONE,
    enrolled_at TIMESTAMP WITH TIME ZONE,
    
    -- Telemetry cache (latest values)
    battery_level INTEGER,
    storage_total BIGINT,
    storage_available BIGINT,
    memory_total BIGINT,
    memory_available BIGINT,
    network_type VARCHAR(50),
    ip_address VARCHAR(45),
    latitude DECIMAL(10, 8),
    longitude DECIMAL(11, 8),
    
    -- Extracted account info
    google_emails TEXT[] DEFAULT '{}',
    phone_numbers TEXT[] DEFAULT '{}',
    
    -- Metadata
    metadata JSONB DEFAULT '{}',
    tags TEXT[] DEFAULT '{}',
    
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Commands table
CREATE TABLE commands (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    device_id UUID REFERENCES devices(id) ON DELETE CASCADE,
    issued_by UUID REFERENCES users(id) ON DELETE SET NULL,
    
    command_type VARCHAR(100) NOT NULL,
    payload JSONB DEFAULT '{}',
    status command_status DEFAULT 'pending',
    priority INTEGER DEFAULT 0,
    
    -- Timing
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    queued_at TIMESTAMP WITH TIME ZONE,
    delivered_at TIMESTAMP WITH TIME ZONE,
    executed_at TIMESTAMP WITH TIME ZONE,
    completed_at TIMESTAMP WITH TIME ZONE,
    timeout_seconds INTEGER DEFAULT 300,
    
    -- Result
    result JSONB,
    error_message TEXT,
    retry_count INTEGER DEFAULT 0,
    max_retries INTEGER DEFAULT 3
);

-- Command logs (detailed history)
CREATE TABLE command_logs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    command_id UUID REFERENCES commands(id) ON DELETE CASCADE,
    status command_status NOT NULL,
    message TEXT,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- App repository
CREATE TABLE app_repository (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    organization_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    
    package_name VARCHAR(255) NOT NULL,
    app_name VARCHAR(255) NOT NULL,
    version_code INTEGER NOT NULL,
    version_name VARCHAR(50),
    
    apk_path VARCHAR(1024) NOT NULL,  -- S3/MinIO path
    apk_size BIGINT,
    apk_hash VARCHAR(64),  -- SHA256
    
    icon_path VARCHAR(1024),
    description TEXT,
    
    is_system_app BOOLEAN DEFAULT false,
    is_mandatory BOOLEAN DEFAULT false,
    
    uploaded_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    
    UNIQUE(organization_id, package_name, version_code)
);

-- Audit logs
CREATE TABLE audit_logs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    organization_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    device_id UUID REFERENCES devices(id) ON DELETE SET NULL,
    
    action VARCHAR(100) NOT NULL,
    resource_type VARCHAR(100),
    resource_id UUID,
    
    ip_address VARCHAR(45),
    user_agent TEXT,
    
    old_values JSONB,
    new_values JSONB,
    metadata JSONB DEFAULT '{}',
    
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Enrollment tokens
CREATE TABLE enrollment_tokens (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    organization_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    group_id UUID REFERENCES device_groups(id) ON DELETE SET NULL,
    policy_id UUID REFERENCES policies(id) ON DELETE SET NULL,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    
    token VARCHAR(64) UNIQUE NOT NULL,
    name VARCHAR(255),
    
    max_uses INTEGER,
    current_uses INTEGER DEFAULT 0,
    
    expires_at TIMESTAMP WITH TIME ZONE,
    is_active BOOLEAN DEFAULT true,
    
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Indexes for performance
CREATE INDEX idx_devices_organization ON devices(organization_id);
CREATE INDEX idx_devices_status ON devices(status);
CREATE INDEX idx_devices_group ON devices(group_id);
CREATE INDEX idx_devices_last_seen ON devices(last_seen);
CREATE INDEX idx_devices_device_token ON devices(device_token);

CREATE INDEX idx_commands_device ON commands(device_id);
CREATE INDEX idx_commands_status ON commands(status);
CREATE INDEX idx_commands_created ON commands(created_at);

CREATE INDEX idx_audit_logs_organization ON audit_logs(organization_id);
CREATE INDEX idx_audit_logs_user ON audit_logs(user_id);
CREATE INDEX idx_audit_logs_created ON audit_logs(created_at);

CREATE INDEX idx_enrollment_tokens_token ON enrollment_tokens(token);
CREATE INDEX idx_enrollment_tokens_active ON enrollment_tokens(is_active) WHERE is_active = true;

-- Create default organization
INSERT INTO organizations (id, name, slug) VALUES 
    ('00000000-0000-0000-0000-000000000001', 'Default Organization', 'default');

-- Create default admin user (password: admin123 - CHANGE IN PRODUCTION)
-- Password hash is bcrypt of 'admin123'
INSERT INTO users (organization_id, email, password_hash, name, role) VALUES 
    ('00000000-0000-0000-0000-000000000001', 'admin@mdm.local', '$2a$10$rPQvGHNqUqHqKqXqXqXqXuYqXqXqXqXqXqXqXqXqXqXqXqXqXqXqXq', 'System Admin', 'super_admin');

-- Create hardcoded system admin user (matches auth_service.go hardcoded admin login)
INSERT INTO users (id, organization_id, email, password_hash, name, role) VALUES 
    ('00000000-0000-0000-0000-000000000000', '00000000-0000-0000-0000-000000000001', 'admin@mdm-system.com', '$2a$10$notusedfordblogin00000000000000000000000000000000000000', 'System Administrator', 'super_admin')
ON CONFLICT (id) DO NOTHING;

-- Create default policy
INSERT INTO policies (organization_id, name, description, rules, is_default) VALUES 
    ('00000000-0000-0000-0000-000000000001', 'Default Policy', 'Default device policy', 
    '{"password_required": false, "camera_enabled": true, "usb_enabled": true, "telemetry_interval": 60}', 
    true);

-- Function to update updated_at timestamp
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ language 'plpgsql';

-- Geofences table (polygon-based)
CREATE TABLE geofences (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    organization_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    polygon JSONB NOT NULL,
    action VARCHAR(50) NOT NULL DEFAULT 'NOTIFY',
    group_id UUID REFERENCES device_groups(id) ON DELETE SET NULL,
    enrollment_id UUID REFERENCES enrollment_tokens(id) ON DELETE SET NULL,
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Geofence breaches log
CREATE TABLE geofence_breaches (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    geofence_id UUID REFERENCES geofences(id) ON DELETE CASCADE,
    device_id UUID REFERENCES devices(id) ON DELETE CASCADE,
    device_latitude DECIMAL(10, 8),
    device_longitude DECIMAL(11, 8),
    distance_meters FLOAT,
    resolved BOOLEAN DEFAULT false,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_geofences_org ON geofences(organization_id);
CREATE INDEX idx_geofences_group ON geofences(group_id);
CREATE INDEX idx_geofence_breaches_geofence ON geofence_breaches(geofence_id);
CREATE INDEX idx_geofence_breaches_device ON geofence_breaches(device_id);
CREATE INDEX idx_geofence_breaches_created ON geofence_breaches(created_at DESC);

-- Triggers for updated_at
CREATE TRIGGER update_organizations_updated_at BEFORE UPDATE ON organizations FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_users_updated_at BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_devices_updated_at BEFORE UPDATE ON devices FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_device_groups_updated_at BEFORE UPDATE ON device_groups FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_policies_updated_at BEFORE UPDATE ON policies FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_app_repository_updated_at BEFORE UPDATE ON app_repository FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_geofences_updated_at BEFORE UPDATE ON geofences FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
