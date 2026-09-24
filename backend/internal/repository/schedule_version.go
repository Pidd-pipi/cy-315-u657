package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/gbschedule/gbschedule/internal/constants"
	"github.com/gbschedule/gbschedule/internal/model"
	"gorm.io/gorm"
)

// ScheduleVersionRepository persists published timetable versions and performs
// the atomic draft -> live promotion on publish.
type ScheduleVersionRepository interface {
	// Publish atomically archives the current live timetable (when it is not
	// tracked by a version yet), replaces the requested weeks with the draft
	// entries, snapshots the resulting live timetable as the new current
	// version and removes the published draft entries.
	Publish(ctx context.Context, weeks []uint, publishedAt time.Time, note string) (model.ScheduleVersion, error)
	GetByID(ctx context.Context, id uint) (*model.ScheduleVersion, error)
	List(ctx context.Context, page, pageSize int) ([]model.ScheduleVersion, int64, error)
	ListEntries(ctx context.Context, versionID uint, week *uint) ([]model.ScheduleVersionEntry, error)
}

type scheduleVersionRepository struct {
	db *gorm.DB
}

// NewScheduleVersionRepository constructs a schedule version repository.
func NewScheduleVersionRepository(db *gorm.DB) ScheduleVersionRepository {
	return &scheduleVersionRepository{db: db}
}

func (r *scheduleVersionRepository) Publish(ctx context.Context, weeks []uint, publishedAt time.Time, note string) (model.ScheduleVersion, error) {
	var result model.ScheduleVersion
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var drafts []model.DraftSchedule
		if err := tx.Where("week IN ?", weeks).Order("id ASC").Find(&drafts).Error; err != nil {
			return fmt.Errorf("load draft schedules for publish: %w", err)
		}
		if len(drafts) == 0 {
			return ErrNoDraft
		}

		var current model.ScheduleVersion
		currentErr := tx.Where("status = ?", constants.VersionCurrent).Order("version DESC").First(&current).Error
		if currentErr != nil && currentErr != gorm.ErrRecordNotFound {
			return fmt.Errorf("load current schedule version: %w", currentErr)
		}
		hasCurrent := currentErr == nil

		var legacyLive []model.Schedule
		if err := tx.Where("1 = 1").Order("id ASC").Find(&legacyLive).Error; err != nil {
			return fmt.Errorf("load live schedules for publish: %w", err)
		}

		nextVersion := 1
		if hasCurrent {
			if err := tx.Model(&model.ScheduleVersion{}).Where("id = ?", current.ID).
				Update("status", constants.VersionArchived).Error; err != nil {
				return fmt.Errorf("archive previous version: %w", err)
			}
			nextVersion = current.Version + 1
		} else if len(legacyLive) > 0 {
			// Legacy live timetable created before versioning existed:
			// archive it before promoting the draft.
			archived := model.ScheduleVersion{
				Version:     1,
				Status:      constants.VersionArchived,
				EntryCount:  len(legacyLive),
				PublishedAt: publishedAt,
				Note:        "legacy timetable archived on first publish",
			}
			if err := tx.Create(&archived).Error; err != nil {
				return fmt.Errorf("archive legacy version: %w", err)
			}
			if err := createVersionEntries(tx, archived.ID, liveToVersionEntries(archived.ID, legacyLive)); err != nil {
				return err
			}
			nextVersion = 2
		}

		if err := tx.Where("week IN ?", weeks).Delete(&model.Schedule{}).Error; err != nil {
			return fmt.Errorf("replace live schedules: %w", err)
		}

		newLive := draftsToLiveSchedules(drafts)
		if err := tx.CreateInBatches(newLive, 200).Error; err != nil {
			return fmt.Errorf("promote draft schedules: %w", err)
		}

		publishedWeekSet := make(map[uint]bool, len(weeks))
		for _, w := range weeks {
			publishedWeekSet[w] = true
		}
		entries := make([]model.ScheduleVersionEntry, 0, len(legacyLive)+len(newLive))
		for i := range legacyLive {
			if publishedWeekSet[legacyLive[i].Week] {
				continue
			}
			entries = append(entries, liveEntryToVersionEntry(0, legacyLive[i]))
		}
		entries = append(entries, liveToVersionEntries(0, newLive)...)

		version := model.ScheduleVersion{
			Version:     nextVersion,
			Status:      constants.VersionCurrent,
			EntryCount:  len(entries),
			PublishedAt: publishedAt,
			Note:        note,
		}
		if err := tx.Create(&version).Error; err != nil {
			return fmt.Errorf("create schedule version: %w", err)
		}
		for i := range entries {
			entries[i].VersionID = version.ID
		}
		if err := createVersionEntries(tx, version.ID, entries); err != nil {
			return err
		}

		if err := tx.Where("week IN ?", weeks).Delete(&model.DraftSchedule{}).Error; err != nil {
			return fmt.Errorf("remove published draft schedules: %w", err)
		}

		result = version
		return nil
	})
	if err != nil {
		return model.ScheduleVersion{}, err
	}
	return result, nil
}

func (r *scheduleVersionRepository) GetByID(ctx context.Context, id uint) (*model.ScheduleVersion, error) {
	var item model.ScheduleVersion
	if err := r.db.WithContext(ctx).First(&item, id).Error; err != nil {
		return nil, normalizeError(err)
	}
	return &item, nil
}

func (r *scheduleVersionRepository) List(ctx context.Context, page, pageSize int) ([]model.ScheduleVersion, int64, error) {
	var items []model.ScheduleVersion
	var total int64
	if err := r.db.WithContext(ctx).Model(&model.ScheduleVersion{}).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count schedule versions: %w", err)
	}
	if err := paginate(r.db.WithContext(ctx).Model(&model.ScheduleVersion{}), page, pageSize).
		Order("version DESC").Find(&items).Error; err != nil {
		return nil, 0, fmt.Errorf("list schedule versions: %w", err)
	}
	return items, total, nil
}

func (r *scheduleVersionRepository) ListEntries(ctx context.Context, versionID uint, week *uint) ([]model.ScheduleVersionEntry, error) {
	query := r.db.WithContext(ctx).Model(&model.ScheduleVersionEntry{}).Where("version_id = ?", versionID)
	if week != nil {
		query = query.Where("week = ?", *week)
	}
	var items []model.ScheduleVersionEntry
	if err := query.Order("week ASC, day_of_week ASC, time_slot_id ASC").Find(&items).Error; err != nil {
		return nil, fmt.Errorf("list schedule version entries: %w", err)
	}
	return items, nil
}

func createVersionEntries(tx *gorm.DB, versionID uint, entries []model.ScheduleVersionEntry) error {
	if len(entries) == 0 {
		return nil
	}
	for i := range entries {
		entries[i].VersionID = versionID
	}
	if err := tx.CreateInBatches(entries, 200).Error; err != nil {
		return fmt.Errorf("create schedule version entries: %w", err)
	}
	return nil
}

func draftsToLiveSchedules(drafts []model.DraftSchedule) []model.Schedule {
	out := make([]model.Schedule, 0, len(drafts))
	for _, d := range drafts {
		out = append(out, model.Schedule{
			Week:        d.Week,
			DayOfWeek:   d.DayOfWeek,
			TimeSlotID:  d.TimeSlotID,
			ClassroomID: d.ClassroomID,
			TeacherID:   d.TeacherID,
			ClassID:     d.ClassID,
			CourseID:    d.CourseID,
		})
	}
	return out
}

func liveToVersionEntries(versionID uint, items []model.Schedule) []model.ScheduleVersionEntry {
	out := make([]model.ScheduleVersionEntry, 0, len(items))
	for _, item := range items {
		out = append(out, liveEntryToVersionEntry(versionID, item))
	}
	return out
}

func liveEntryToVersionEntry(versionID uint, item model.Schedule) model.ScheduleVersionEntry {
	return model.ScheduleVersionEntry{
		VersionID:      versionID,
		LiveScheduleID: item.ID,
		Week:           item.Week,
		DayOfWeek:      item.DayOfWeek,
		TimeSlotID:     item.TimeSlotID,
		ClassroomID:    item.ClassroomID,
		TeacherID:      item.TeacherID,
		ClassID:        item.ClassID,
		CourseID:       item.CourseID,
	}
}
