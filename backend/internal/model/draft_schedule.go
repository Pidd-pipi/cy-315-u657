package model

import "gorm.io/gorm"

// DraftSchedule is an unpublished timetable entry. Generated timetables and
// swap/move adjustments are staged here until an explicit publish promotes
// them into the live schedules table.
type DraftSchedule struct {
	gorm.Model
	Week             uint `gorm:"index;not null" json:"week"`
	DayOfWeek        int  `gorm:"index;not null" json:"day_of_week"`
	TimeSlotID       uint `gorm:"index;not null" json:"time_slot_id"`
	ClassroomID      uint `gorm:"index;not null" json:"classroom_id"`
	TeacherID        uint `gorm:"index;not null" json:"teacher_id"`
	ClassID          uint `gorm:"index;not null" json:"class_id"`
	CourseID         uint `gorm:"index;not null" json:"course_id"`
	SourceScheduleID uint `gorm:"index" json:"source_schedule_id"`
}

// TableName explicitly names the draft timetable table.
func (DraftSchedule) TableName() string { return "draft_schedules" }
