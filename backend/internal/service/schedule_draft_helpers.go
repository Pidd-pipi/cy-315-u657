package service

import (
	"github.com/gbschedule/gbschedule/internal/dto"
	"github.com/gbschedule/gbschedule/internal/model"
)

func draftScheduleFromLive(item model.Schedule, sourceScheduleID uint) model.DraftSchedule {
	return model.DraftSchedule{
		Week:             item.Week,
		DayOfWeek:        item.DayOfWeek,
		TimeSlotID:       item.TimeSlotID,
		ClassroomID:      item.ClassroomID,
		TeacherID:        item.TeacherID,
		ClassID:          item.ClassID,
		CourseID:         item.CourseID,
		SourceScheduleID: sourceScheduleID,
	}
}

func draftToSchedule(item model.DraftSchedule) model.Schedule {
	s := model.Schedule{
		Week:        item.Week,
		DayOfWeek:   item.DayOfWeek,
		TimeSlotID:  item.TimeSlotID,
		ClassroomID: item.ClassroomID,
		TeacherID:   item.TeacherID,
		ClassID:     item.ClassID,
		CourseID:    item.CourseID,
	}
	// Keep the draft row ID so conflict detection distinguishes entries and
	// the response can reference the draft entry consistently.
	s.ID = item.ID
	return s
}

func draftsToSchedules(items []model.DraftSchedule) []model.Schedule {
	out := make([]model.Schedule, 0, len(items))
	for _, item := range items {
		out = append(out, draftToSchedule(item))
	}
	return out
}

func versionEntryToSchedule(item model.ScheduleVersionEntry) model.Schedule {
	return model.Schedule{
		Week:        item.Week,
		DayOfWeek:   item.DayOfWeek,
		TimeSlotID:  item.TimeSlotID,
		ClassroomID: item.ClassroomID,
		TeacherID:   item.TeacherID,
		ClassID:     item.ClassID,
		CourseID:    item.CourseID,
	}
}

func versionEntriesToSchedules(items []model.ScheduleVersionEntry) []model.Schedule {
	out := make([]model.Schedule, 0, len(items))
	for _, item := range items {
		s := versionEntryToSchedule(item)
		if item.LiveScheduleID != 0 {
			s.ID = item.LiveScheduleID
		}
		out = append(out, s)
	}
	return out
}

func filterSchedules(items []model.Schedule, classID, teacherID, classroomID *uint) []model.Schedule {
	out := make([]model.Schedule, 0, len(items))
	for _, item := range items {
		if classID != nil && item.ClassID != *classID {
			continue
		}
		if teacherID != nil && item.TeacherID != *teacherID {
			continue
		}
		if classroomID != nil && item.ClassroomID != *classroomID {
			continue
		}
		out = append(out, item)
	}
	return out
}

func filterConflictsByWeeks(conflicts []dto.ConflictResponse, weeks []uint) []dto.ConflictResponse {
	allowed := make(map[uint]bool, len(weeks))
	for _, w := range weeks {
		allowed[w] = true
	}
	out := make([]dto.ConflictResponse, 0, len(conflicts))
	for _, c := range conflicts {
		if allowed[c.Week] {
			out = append(out, c)
		}
	}
	return out
}

func versionResponse(item model.ScheduleVersion) dto.ScheduleVersionResponse {
	return dto.ScheduleVersionResponse{
		ID:          item.ID,
		Version:     item.Version,
		Status:      item.Status,
		EntryCount:  item.EntryCount,
		PublishedAt: item.PublishedAt.Format("2006-01-02 15:04:05"),
		Note:        item.Note,
		CreatedAt:   item.CreatedAt.Format("2006-01-02 15:04:05"),
	}
}
