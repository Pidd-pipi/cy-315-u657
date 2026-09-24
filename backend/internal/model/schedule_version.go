package model

import "gorm.io/gorm"

// ScheduleVersion is an immutable snapshot of the official timetable produced
// by a successful publish. Old published schedule rows keep pointing at their
// original version, while queries and exports default to the latest version
// (per week when a publish only covered selected weeks).
type ScheduleVersion struct {
	gorm.Model
	Version    int    `gorm:"uniqueIndex;not null" json:"version"`
	Weeks      string `gorm:"size:255;not null;default:''" json:"weeks"`
	WeekCount  int    `gorm:"not null;default:0" json:"week_count"`
	EntryCount int    `gorm:"not null;default:0" json:"entry_count"`
	Note       string `gorm:"type:text" json:"note"`
}

// TableName explicitly names the table.
func (ScheduleVersion) TableName() string { return "schedule_versions" }
