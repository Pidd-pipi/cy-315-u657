package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	_ "github.com/gbschedule/gbschedule/docs"
	"github.com/gbschedule/gbschedule/internal/config"
	"github.com/gbschedule/gbschedule/internal/handler"
	"github.com/gbschedule/gbschedule/internal/model"
	"github.com/gbschedule/gbschedule/internal/repository"
	"github.com/gbschedule/gbschedule/internal/router"
	"github.com/gbschedule/gbschedule/internal/service"
)

// @title           教室排课助手 API
// @version         1.0.0
// @description     教室排课、教室资源管理和冲突检测 RESTful API。
// @BasePath        /
func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	logger := newLogger(cfg.LogLevel)
	db, err := openDatabase(cfg.DBPath)
	if err != nil {
		logger.Error("open database", slog.String("error", err.Error()))
		os.Exit(1)
	}
	if err := migrate(db); err != nil {
		logger.Error("migrate database", slog.String("error", err.Error()))
		os.Exit(1)
	}

	app, err := newApp(db, logger)
	if err != nil {
		logger.Error("assemble app", slog.String("error", err.Error()))
		os.Exit(1)
	}

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.ServerPort),
		Handler:      app,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	logger.Info("server starting", slog.Int("port", cfg.ServerPort), slog.String("db_path", cfg.DBPath))
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server stopped", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func newLogger(level string) *slog.Logger {
	var slogLevel slog.Level
	switch level {
	case "debug":
		slogLevel = slog.LevelDebug
	case "warn":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	default:
		slogLevel = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slogLevel}))
}

func openDatabase(path string) (*gorm.DB, error) {
	if path == "" {
		path = "./data/gbschedule.db"
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger:         gormlogger.Default.LogMode(gormlogger.Silent),
		TranslateError: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	return db, nil
}

func migrate(db *gorm.DB) error {
	if err := db.AutoMigrate(
		&model.Classroom{},
		&model.Teacher{},
		&model.Class{},
		&model.Course{},
		&model.TimeSlot{},
		&model.Schedule{},
		&model.ScheduleVersion{},
		&model.AdjustmentLog{},
	); err != nil {
		return fmt.Errorf("auto migrate: %w", err)
	}
	if err := backfillLegacySchedules(db); err != nil {
		return fmt.Errorf("backfill legacy schedules: %w", err)
	}
	return nil
}

// backfillLegacySchedules treats rows written before the draft/publish feature
// (status/version_id added by AutoMigrate) as the first official version, so
// existing deployments keep their current timetable queryable.
func backfillLegacySchedules(db *gorm.DB) error {
	var legacyCount int64
	if err := db.Model(&model.Schedule{}).Where("version_id IS NULL").Count(&legacyCount).Error; err != nil {
		return fmt.Errorf("count legacy schedules: %w", err)
	}
	if legacyCount == 0 {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var version model.ScheduleVersion
		err := tx.Where("version = ?", 1).First(&version).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			version = model.ScheduleVersion{Version: 1, Note: "initial version (pre-draft migration)"}
			if err := tx.Create(&version).Error; err != nil {
				return fmt.Errorf("create initial version: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("load initial version: %w", err)
		}
		var weekCount int64
		if err := tx.Model(&model.Schedule{}).Where("version_id IS NULL").Distinct("week").Count(&weekCount).Error; err != nil {
			return fmt.Errorf("count legacy weeks: %w", err)
		}
		if err := tx.Model(&model.ScheduleVersion{}).Where("id = ?", version.ID).
			Updates(map[string]any{"entry_count": legacyCount, "week_count": weekCount}).Error; err != nil {
			return fmt.Errorf("update initial version counts: %w", err)
		}
		if err := tx.Model(&model.Schedule{}).Where("version_id IS NULL").
			Update("version_id", version.ID).Error; err != nil {
			return fmt.Errorf("attach legacy schedules to version: %w", err)
		}
		return nil
	})
}

func newApp(db *gorm.DB, logger *slog.Logger) (*gin.Engine, error) {
	classroomRepo := repository.NewClassroomRepository(db)
	teacherRepo := repository.NewTeacherRepository(db)
	classRepo := repository.NewClassRepository(db)
	courseRepo := repository.NewCourseRepository(db)
	timeSlotRepo := repository.NewTimeSlotRepository(db)
	scheduleRepo := repository.NewScheduleRepository(db)
	versionRepo := repository.NewScheduleVersionRepository(db)
	adjustmentRepo := repository.NewAdjustmentLogRepository(db)
	uow := repository.NewUnitOfWork(db)

	classroomService := service.NewClassroomService(classroomRepo, logger)
	teacherService := service.NewTeacherService(teacherRepo, logger)
	classService := service.NewClassService(classRepo, logger)
	courseService := service.NewCourseService(courseRepo, logger)
	timeSlotService := service.NewTimeSlotService(timeSlotRepo, logger)
	scheduleService := service.NewScheduleService(scheduleRepo, versionRepo, classroomRepo, teacherRepo, classRepo, courseRepo, timeSlotRepo, adjustmentRepo, uow, logger)

	h := router.Handlers{
		Classroom:  handler.NewClassroomHandler(classroomService, logger),
		Teacher:    handler.NewTeacherHandler(teacherService, logger),
		Class:      handler.NewClassHandler(classService, logger),
		Course:     handler.NewCourseHandler(courseService, logger),
		TimeSlot:   handler.NewTimeSlotHandler(timeSlotService, logger),
		Schedule:   handler.NewScheduleHandler(scheduleService, logger),
		Statistics: handler.NewStatisticsHandler(scheduleService, logger),
	}
	return router.New(h, logger), nil
}
