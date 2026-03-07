-- Migration: Add is_device_locked column to devices table
-- Tracks whether the device screen is locked (keyguard active)
-- When locked, remote commands may fail since we're not device owners

ALTER TABLE devices ADD COLUMN IF NOT EXISTS is_device_locked BOOLEAN DEFAULT NULL;
