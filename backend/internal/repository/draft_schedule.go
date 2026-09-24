package repository

import (
	"context"
	"fmt"

	"github.com/gbschedule/gbschedule/internal/model"
	"gorm.io/gorm"
)

// DraftScheduleFilter contains optional query filters for draft entries.
type DraftScheduleFilter struct {
	Week        *uint
	ClassID     *uint
	TeacherID   *uint
	ClassroomID *uint
}

// DraftScheduleRepository defines persistence operations for the unpublished
// (draft) timetable.
type DraftScheduleRepository interface {
	CreateBatch(ctx context.Context, drafts []model.DraftSchedule) error
	GetByID(ctx context.Context, id uint) (*model.DraftSchedule, error)
	List(ctx context.Context, filter DraftScheduleFilter) ([]model.DraftSchedule, error)
	Update(ctx context.Context, draft *model.DraftSchedule) error
	DeleteAll(ctx context.Context) error
	DeleteByWeeks(ctx context.Context, weeks []uint) error
}

type draftScheduleRepository struct {
	db *gorm.DB
}

// NewDraftScheduleRepository constructs a draft schedule repository.
func NewDraftScheduleRepository(db *gorm.DB) DraftScheduleRepository {
	return &draftScheduleRepository{db: db}
}

func (r *draftScheduleRepository) CreateBatch(ctx context.Context, drafts []model.DraftSchedule) error {
	if len(drafts) == 0 {
		return nil
	}
	if err := r.db.WithContext(ctx).CreateInBatches(drafts, 200).Error; err != nil {
		return fmt.Errorf("create draft schedules: %w", err)
	}
	return nil
}

func (r *draftScheduleRepository) GetByID(ctx context.Context, id uint) (*model.DraftSchedule, error) {
	var item model.DraftSchedule
	if err := r.db.WithContext(ctx).First(&item, id).Error; err != nil {
		return nil, normalizeError(err)
	}
	return &item, nil
}

func (r *draftScheduleRepository) List(ctx context.Context, filter DraftScheduleFilter) ([]model.DraftSchedule, error) {
	query := r.db.WithContext(ctx).Model(&model.DraftSchedule{})
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
	var items []model.DraftSchedule
	if err := query.Order("week ASC, day_of_week ASC, time_slot_id ASC").Find(&items).Error; err != nil {
		return nil, fmt.Errorf("list draft schedules: %w", err)
	}
	return items, nil
}

func (r *draftScheduleRepository) Update(ctx context.Context, draft *model.DraftSchedule) error {
	if err := r.db.WithContext(ctx).Save(draft).Error; err != nil {
		return fmt.Errorf("update draft schedule: %w", err)
	}
	return nil
}

func (r *draftScheduleRepository) DeleteAll(ctx context.Context) error {
	if err := r.db.WithContext(ctx).Where("1 = 1").Delete(&model.DraftSchedule{}).Error; err != nil {
		return fmt.Errorf("delete all draft schedules: %w", err)
	}
	return nil
}

func (r *draftScheduleRepository) DeleteByWeeks(ctx context.Context, weeks []uint) error {
	if len(weeks) == 0 {
		return nil
	}
	if err := r.db.WithContext(ctx).Where("week IN ?", weeks).Delete(&model.DraftSchedule{}).Error; err != nil {
		return fmt.Errorf("delete draft schedules by weeks: %w", err)
	}
	return nil
}
