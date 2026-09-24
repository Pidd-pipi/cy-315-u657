package repository

import (
	"context"
	"fmt"

	"github.com/gbschedule/gbschedule/internal/model"
	"gorm.io/gorm"
)

// ScheduleFilter contains optional query filters for timetable entries.
type ScheduleFilter struct {
	Week        *uint
	ClassID     *uint
	TeacherID   *uint
	ClassroomID *uint
	// Status filters by lifecycle status (draft/published).
	Status *string
	// VersionID filters entries of one specific published version.
	VersionID *uint
	// CurrentOnly restricts published entries to the latest version per week.
	CurrentOnly bool
}

// ScheduleRepository defines persistence operations for timetable entries.
type ScheduleRepository interface {
	CreateBatch(ctx context.Context, schedules []model.Schedule) error
	GetByID(ctx context.Context, id uint) (*model.Schedule, error)
	GetDraftByID(ctx context.Context, id uint) (*model.Schedule, error)
	List(ctx context.Context, filter ScheduleFilter) ([]model.Schedule, error)
	Update(ctx context.Context, schedule *model.Schedule) error
	DeleteByID(ctx context.Context, id uint) error

	// Draft and publish workflow helpers.
	DeleteAllByStatus(ctx context.Context, status string) error
	CountByStatus(ctx context.Context, status string, weeks []uint) (int64, error)
	// PromoteDraft flips draft entries (optionally restricted to weeks) into
	// the given published version. Published rows are never modified, so all
	// previous official arrangements remain in the version history.
	PromoteDraft(ctx context.Context, weeks []uint, versionID uint) (int64, error)
}

type scheduleRepository struct {
	db *gorm.DB
}

// NewScheduleRepository constructs a schedule repository.
func NewScheduleRepository(db *gorm.DB) ScheduleRepository {
	return &scheduleRepository{db: db}
}

func (r *scheduleRepository) CreateBatch(ctx context.Context, schedules []model.Schedule) error {
	if len(schedules) == 0 {
		return nil
	}
	if err := r.db.WithContext(ctx).CreateInBatches(schedules, 200).Error; err != nil {
		return fmt.Errorf("create schedules: %w", err)
	}
	return nil
}

func (r *scheduleRepository) GetByID(ctx context.Context, id uint) (*model.Schedule, error) {
	var item model.Schedule
	if err := r.db.WithContext(ctx).First(&item, id).Error; err != nil {
		return nil, normalizeError(err)
	}
	return &item, nil
}

func (r *scheduleRepository) GetDraftByID(ctx context.Context, id uint) (*model.Schedule, error) {
	var item model.Schedule
	if err := r.db.WithContext(ctx).Where("status = ?", "draft").First(&item, id).Error; err != nil {
		return nil, normalizeError(err)
	}
	return &item, nil
}

func (r *scheduleRepository) List(ctx context.Context, filter ScheduleFilter) ([]model.Schedule, error) {
	query := r.applyFilter(r.db.WithContext(ctx).Model(&model.Schedule{}), filter)
	var items []model.Schedule
	if err := query.Order("week ASC, day_of_week ASC, time_slot_id ASC").Find(&items).Error; err != nil {
		return nil, fmt.Errorf("list schedules: %w", err)
	}
	return items, nil
}

func (r *scheduleRepository) applyFilter(query *gorm.DB, filter ScheduleFilter) *gorm.DB {
	if filter.Week != nil {
		query = query.Where("week = ?", *filter.Week)
	}
	if filter.ClassID != nil {
		query = query.Where("class_id = ?", *filter.ClassID)
	}
	if filter.TeacherID != nil {
		query = query.Where("teacher_id = ?", *filter.TeacherID)
	}
	if filter.ClassroomID != nil {
		query = query.Where("classroom_id = ?", *filter.ClassroomID)
	}
	if filter.Status != nil {
		query = query.Where("status = ?", *filter.Status)
	}
	if filter.VersionID != nil {
		query = query.Where("version_id = ?", *filter.VersionID)
	}
	if filter.CurrentOnly {
		// Latest published version covering each week.
		latestVersion := r.db.Table("schedules AS s2").
			Select("MAX(s2.version_id)").
			Where("s2.week = schedules.week AND s2.status = ?", "published")
		query = query.Where("status = ? AND version_id = (?)", "published", latestVersion)
	}
	return query
}

func (r *scheduleRepository) Update(ctx context.Context, schedule *model.Schedule) error {
	if err := r.db.WithContext(ctx).Save(schedule).Error; err != nil {
		return fmt.Errorf("update schedule: %w", err)
	}
	return nil
}

func (r *scheduleRepository) DeleteByID(ctx context.Context, id uint) error {
	if err := r.db.WithContext(ctx).Delete(&model.Schedule{}, id).Error; err != nil {
		return fmt.Errorf("delete schedule: %w", err)
	}
	return nil
}

func (r *scheduleRepository) DeleteAllByStatus(ctx context.Context, status string) error {
	if err := r.db.WithContext(ctx).Where("status = ?", status).Delete(&model.Schedule{}).Error; err != nil {
		return fmt.Errorf("delete schedules by status: %w", err)
	}
	return nil
}

func (r *scheduleRepository) CountByStatus(ctx context.Context, status string, weeks []uint) (int64, error) {
	var total int64
	query := r.db.WithContext(ctx).Model(&model.Schedule{}).Where("status = ?", status)
	if len(weeks) > 0 {
		query = query.Where("week IN ?", weeks)
	}
	if err := query.Count(&total).Error; err != nil {
		return 0, fmt.Errorf("count schedules by status: %w", err)
	}
	return total, nil
}

func (r *scheduleRepository) PromoteDraft(ctx context.Context, weeks []uint, versionID uint) (int64, error) {
	query := r.db.WithContext(ctx).Model(&model.Schedule{}).
		Where("status = ?", "draft")
	if len(weeks) > 0 {
		query = query.Where("week IN ?", weeks)
	}
	result := query.Updates(map[string]any{"status": "published", "version_id": versionID})
	if result.Error != nil {
		return 0, fmt.Errorf("promote draft to published: %w", result.Error)
	}
	return result.RowsAffected, nil
}
