-- 教室排课助手 draft/publish workflow (SQLite)
-- Schedules gain a lifecycle status (draft|published) and a pointer to the
-- official version they belong to. Existing rows are treated as the first
-- published version so live deployments keep their current timetable.

ALTER TABLE schedules ADD COLUMN status TEXT NOT NULL DEFAULT 'published';
ALTER TABLE schedules ADD COLUMN version_id INTEGER;

CREATE INDEX IF NOT EXISTS idx_schedules_status ON schedules(status);
CREATE INDEX IF NOT EXISTS idx_schedules_version ON schedules(version_id);

CREATE TABLE IF NOT EXISTS schedule_versions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at DATETIME,
    updated_at DATETIME,
    deleted_at DATETIME,
    version INTEGER NOT NULL UNIQUE,
    weeks TEXT NOT NULL DEFAULT '',
    week_count INTEGER NOT NULL DEFAULT 0,
    entry_count INTEGER NOT NULL DEFAULT 0,
    note TEXT
);

-- Backfill: register v1 and attach every pre-existing official row to it.
INSERT INTO schedule_versions (created_at, updated_at, version, weeks, week_count, entry_count, note)
SELECT CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 1, '',
       (SELECT COUNT(DISTINCT week) FROM schedules),
       (SELECT COUNT(*) FROM schedules),
       'initial version (pre-draft migration)'
WHERE EXISTS (SELECT 1 FROM schedules LIMIT 1)
  AND NOT EXISTS (SELECT 1 FROM schedule_versions WHERE version = 1);

UPDATE schedules SET version_id = (SELECT id FROM schedule_versions WHERE version = 1)
WHERE version_id IS NULL
  AND EXISTS (SELECT 1 FROM schedule_versions WHERE version = 1);
