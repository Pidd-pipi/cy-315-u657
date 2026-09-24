package repository

import (
	"context"
	"fmt"

	"github.com/gbschedule/gbschedule/internal/model"
	"gorm.io/gorm"
)

// ScheduleVersionRepository persists official timetable versions.
type ScheduleVersionRepository interface {
	Create(ctx context.Context, version *model.ScheduleVersion) error
	Update(ctx context.Context, version *model.ScheduleVersion) error
	GetByID(ctx context.Context, id uint) (*model.ScheduleVersion, error)
	List(ctx context.Context, page, pageSize int) ([]model.ScheduleVersion, int64, error)
	// Latest returns the most recently created version, or ErrNotFound when no
	// version exists yet.
	Latest(ctx context.Context) (*model.ScheduleVersion, error)
}

type scheduleVersionRepository struct {
	db *gorm.DB
}

// NewScheduleVersionRepository constructs a schedule version repository.
func NewScheduleVersionRepository(db *gorm.DB) ScheduleVersionRepository {
	return &scheduleVersionRepository{db: db}
}

func (r *scheduleVersionRepository) Create(ctx context.Context, version *model.ScheduleVersion) error {
	if err := r.db.WithContext(ctx).Create(version).Error; err != nil {
		return fmt.Errorf("create schedule version: %w", err)
	}
	return nil
}

func (r *scheduleVersionRepository) Update(ctx context.Context, version *model.ScheduleVersion) error {
	if err := r.db.WithContext(ctx).Save(version).Error; err != nil {
		return fmt.Errorf("update schedule version: %w", err)
	}
	return nil
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

func (r *scheduleVersionRepository) Latest(ctx context.Context) (*model.ScheduleVersion, error) {
	var item model.ScheduleVersion
	if err := r.db.WithContext(ctx).Order("version DESC").First(&item).Error; err != nil {
		return nil, normalizeError(err)
	}
	return &item, nil
}
