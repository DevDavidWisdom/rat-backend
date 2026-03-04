-- Migration: Add issam_id column to devices table
-- Stores the agent_id extracted from /storage/emulated/0/Download/tractrac_agent.json

ALTER TABLE devices ADD COLUMN IF NOT EXISTS issam_id TEXT DEFAULT NULL;
