-- Migration: Add google_emails and phone_numbers columns to devices table
-- Run this on existing databases to support the GET_DEVICE_ACCOUNTS feature

ALTER TABLE devices ADD COLUMN IF NOT EXISTS google_emails TEXT[] DEFAULT '{}';
ALTER TABLE devices ADD COLUMN IF NOT EXISTS phone_numbers TEXT[] DEFAULT '{}';
