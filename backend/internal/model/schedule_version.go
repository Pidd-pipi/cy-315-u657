package model

import (
	"time"

	"gorm.io/gorm"
)

// ScheduleVersion is a published timetable snapshot. Every successful publish
// archives the previous live timetable as a historical version, so queries and
// exports can always read the latest published version and older versions stay
// available for review.
type ScheduleVersion struct {
	gorm.Model
	Version     int       `gorm:"uniqueIndex;not null" json:"version"`
	Status      string    `gorm:"size:16;index;not null" json:"status"`
	EntryCount  int       `gorm:"not null" json:"entry_count"`
	PublishedAt time.Time `gorm:"not null" json:"published_at"`
	Note        string    `gorm:"size:256" json:"note"`
}

// TableName explicitly names the schedule version table.
func (ScheduleVersion) TableName() string { return "schedule_versions" }

// ScheduleVersionEntry is one timetable entry belonging to a ScheduleVersion
// snapshot. LiveScheduleID preserves the original live row ID so adjustment
// history references stay traceable.
type ScheduleVersionEntry struct {
	gorm.Model
	VersionID      uint `gorm:"index;not null" json:"version_id"`
	LiveScheduleID uint `gorm:"index" json:"live_schedule_id"`
	Week           uint `gorm:"index;not null" json:"week"`
	DayOfWeek      int  `gorm:"index;not null" json:"day_of_week"`
	TimeSlotID     uint `gorm:"index;not null" json:"time_slot_id"`
	ClassroomID    uint `gorm:"index;not null" json:"classroom_id"`
	TeacherID      uint `gorm:"index;not null" json:"teacher_id"`
	ClassID        uint `gorm:"index;not null" json:"class_id"`
	CourseID       uint `gorm:"index;not null" json:"course_id"`
}

// TableName explicitly names the schedule version entry table.
func (ScheduleVersionEntry) TableName() string { return "schedule_version_entries" }
