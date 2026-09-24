package repository

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// UnitOfWork executes a function atomically. Repositories returned by Repos
// share the transaction, so all writes either commit together or roll back.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context, repos TxRepositories) error) error
}

// TxRepositories are repositories bound to the active transaction.
type TxRepositories struct {
	Schedules        ScheduleRepository
	ScheduleVersions ScheduleVersionRepository
	AdjustmentLogs   AdjustmentLogRepository
}

type unitOfWork struct {
	db *gorm.DB
}

// NewUnitOfWork constructs a GORM-backed unit of work.
func NewUnitOfWork(db *gorm.DB) UnitOfWork {
	return &unitOfWork{db: db}
}

func (u *unitOfWork) Do(ctx context.Context, fn func(ctx context.Context, repos TxRepositories) error) error {
	err := u.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txRepos := TxRepositories{
			Schedules:        NewScheduleRepository(tx),
			ScheduleVersions: NewScheduleVersionRepository(tx),
			AdjustmentLogs:   NewAdjustmentLogRepository(tx),
		}
		return fn(ctx, txRepos)
	})
	if err != nil {
		return fmt.Errorf("transaction: %w", err)
	}
	return nil
}
