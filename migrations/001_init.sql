-- 教室排课助手 initial schema (SQLite)
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS classrooms (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at DATETIME,
    updated_at DATETIME,
    deleted_at DATETIME,
    code TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    capacity INTEGER NOT NULL DEFAULT 0,
    equipment TEXT
);

CREATE TABLE IF NOT EXISTS teachers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at DATETIME,
    updated_at DATETIME,
    deleted_at DATETIME,
    name TEXT NOT NULL,
    employee_no TEXT NOT NULL UNIQUE,
    contact TEXT,
    subjects TEXT,
    unavailable_slots TEXT
);

CREATE TABLE IF NOT EXISTS classes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at DATETIME,
    updated_at DATETIME,
    deleted_at DATETIME,
    name TEXT NOT NULL,
    student_count INTEGER NOT NULL DEFAULT 0,
    grade TEXT
);

CREATE TABLE IF NOT EXISTS courses (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at DATETIME,
    updated_at DATETIME,
    deleted_at DATETIME,
    name TEXT NOT NULL,
    code TEXT NOT NULL UNIQUE,
    duration INTEGER NOT NULL DEFAULT 1,
    room_type TEXT
);

CREATE TABLE IF NOT EXISTS time_slots (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at DATETIME,
    updated_at DATETIME,
    deleted_at DATETIME,
    code TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    start_time TEXT NOT NULL,
    end_time TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS schedules (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at DATETIME,
    updated_at DATETIME,
    deleted_at DATETIME,
    week INTEGER NOT NULL,
    day_of_week INTEGER NOT NULL,
    time_slot_id INTEGER NOT NULL,
    classroom_id INTEGER NOT NULL,
    teacher_id INTEGER NOT NULL,
    class_id INTEGER NOT NULL,
    course_id INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_schedules_week ON schedules(week);
CREATE INDEX IF NOT EXISTS idx_schedules_day ON schedules(day_of_week);
CREATE INDEX IF NOT EXISTS idx_schedules_time_slot ON schedules(time_slot_id);
CREATE INDEX IF NOT EXISTS idx_schedules_classroom ON schedules(classroom_id);
CREATE INDEX IF NOT EXISTS idx_schedules_teacher ON schedules(teacher_id);
CREATE INDEX IF NOT EXISTS idx_schedules_class ON schedules(class_id);
CREATE INDEX IF NOT EXISTS idx_schedules_course ON schedules(course_id);

CREATE TABLE IF NOT EXISTS adjustment_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at DATETIME,
    updated_at DATETIME,
    deleted_at DATETIME,
    schedule_id INTEGER,
    action TEXT NOT NULL,
    detail TEXT
);
CREATE INDEX IF NOT EXISTS idx_adjustment_logs_schedule ON adjustment_logs(schedule_id);

-- Unpublished draft timetable: generated schedules and swap/move adjustments
-- are staged here until they are explicitly published.
CREATE TABLE IF NOT EXISTS draft_schedules (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at DATETIME,
    updated_at DATETIME,
    deleted_at DATETIME,
    week INTEGER NOT NULL,
    day_of_week INTEGER NOT NULL,
    time_slot_id INTEGER NOT NULL,
    classroom_id INTEGER NOT NULL,
    teacher_id INTEGER NOT NULL,
    class_id INTEGER NOT NULL,
    course_id INTEGER NOT NULL,
    source_schedule_id INTEGER
);
CREATE INDEX IF NOT EXISTS idx_draft_schedules_week ON draft_schedules(week);
CREATE INDEX IF NOT EXISTS idx_draft_schedules_day ON draft_schedules(day_of_week);
CREATE INDEX IF NOT EXISTS idx_draft_schedules_time_slot ON draft_schedules(time_slot_id);
CREATE INDEX IF NOT EXISTS idx_draft_schedules_classroom ON draft_schedules(classroom_id);
CREATE INDEX IF NOT EXISTS idx_draft_schedules_teacher ON draft_schedules(teacher_id);
CREATE INDEX IF NOT EXISTS idx_draft_schedules_class ON draft_schedules(class_id);
CREATE INDEX IF NOT EXISTS idx_draft_schedules_course ON draft_schedules(course_id);
CREATE INDEX IF NOT EXISTS idx_draft_schedules_source ON draft_schedules(source_schedule_id);

-- Published timetable version metadata (one current version, older versions archived).
CREATE TABLE IF NOT EXISTS schedule_versions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at DATETIME,
    updated_at DATETIME,
    deleted_at DATETIME,
    version INTEGER NOT NULL UNIQUE,
    status TEXT NOT NULL,
    entry_count INTEGER NOT NULL,
    published_at DATETIME NOT NULL,
    note TEXT
);
CREATE INDEX IF NOT EXISTS idx_schedule_versions_status ON schedule_versions(status);

-- Immutable timetable entries belonging to each published version.
CREATE TABLE IF NOT EXISTS schedule_version_entries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at DATETIME,
    updated_at DATETIME,
    deleted_at DATETIME,
    version_id INTEGER NOT NULL,
    live_schedule_id INTEGER,
    week INTEGER NOT NULL,
    day_of_week INTEGER NOT NULL,
    time_slot_id INTEGER NOT NULL,
    classroom_id INTEGER NOT NULL,
    teacher_id INTEGER NOT NULL,
    class_id INTEGER NOT NULL,
    course_id INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_schedule_version_entries_version ON schedule_version_entries(version_id);
CREATE INDEX IF NOT EXISTS idx_schedule_version_entries_live ON schedule_version_entries(live_schedule_id);
CREATE INDEX IF NOT EXISTS idx_schedule_version_entries_week ON schedule_version_entries(week);
CREATE INDEX IF NOT EXISTS idx_schedule_version_entries_day ON schedule_version_entries(day_of_week);
CREATE INDEX IF NOT EXISTS idx_schedule_version_entries_time_slot ON schedule_version_entries(time_slot_id);
CREATE INDEX IF NOT EXISTS idx_schedule_version_entries_classroom ON schedule_version_entries(classroom_id);
CREATE INDEX IF NOT EXISTS idx_schedule_version_entries_teacher ON schedule_version_entries(teacher_id);
CREATE INDEX IF NOT EXISTS idx_schedule_version_entries_class ON schedule_version_entries(class_id);
CREATE INDEX IF NOT EXISTS idx_schedule_version_entries_course ON schedule_version_entries(course_id);
