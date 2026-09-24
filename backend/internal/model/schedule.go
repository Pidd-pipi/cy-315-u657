package model

import "gorm.io/gorm"

// Schedule is one lesson entry in a timetable.
//
// Entries start life as drafts (Status = "draft", VersionID = nil) after a
// generate/swap/move operation. Publishing turns drafts into an immutable
// official version (Status = "published", VersionID set); published rows are
// never overwritten in place, so every previous official arrangement stays
// queryable in the version history.
type Schedule struct {
	gorm.Model
	Week        uint   `gorm:"index;not null" json:"week"`
	DayOfWeek   int    `gorm:"index;not null" json:"day_of_week"`
	TimeSlotID  uint   `gorm:"index;not null" json:"time_slot_id"`
	ClassroomID uint   `gorm:"index;not null" json:"classroom_id"`
	TeacherID   uint   `gorm:"index;not null" json:"teacher_id"`
	ClassID     uint   `gorm:"index;not null" json:"class_id"`
	CourseID    uint   `gorm:"index;not null" json:"course_id"`
	Status      string `gorm:"size:16;index;not null;default:published" json:"status"`
	VersionID   *uint  `gorm:"index" json:"version_id,omitempty"`
}

// TableName explicitly names the table to avoid GORM's default "schedules".
func (Schedule) TableName() string { return "schedules" }
