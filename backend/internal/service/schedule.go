package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	"github.com/gbschedule/gbschedule/internal/constants"
	"github.com/gbschedule/gbschedule/internal/dto"
	"github.com/gbschedule/gbschedule/internal/model"
	"github.com/gbschedule/gbschedule/internal/repository"
)

// ScheduleService exposes scheduling, conflict detection, adjustment and statistics operations.
type ScheduleService interface {
	// Generate produces a new timetable into the pending draft. The official
	// timetable is untouched until Publish is called.
	Generate(ctx context.Context, req *dto.GenerateScheduleRequest) (*dto.GenerateScheduleResponse, error)
	// List reads the latest official timetable (or a specific version).
	List(ctx context.Context, week, classID, teacherID, classroomID, versionID *uint) ([]dto.ScheduleResponse, error)
	// ListDraft reads the pending draft, optionally filtered by week.
	ListDraft(ctx context.Context, week *uint) (*dto.DraftResponse, error)
	Get(ctx context.Context, id uint) (*dto.ScheduleResponse, error)
	// CheckConflicts reports conflicts in the draft when one exists, otherwise
	// in the current official timetable.
	CheckConflicts(ctx context.Context, week *uint) ([]dto.ConflictResponse, error)
	// Publish promotes the draft (optionally selected weeks) into a new official
	// version. It fails with DraftConflictError when conflicts remain.
	Publish(ctx context.Context, req *dto.PublishScheduleRequest, weekFilter *uint) (*dto.PublishScheduleResponse, error)
	ListVersions(ctx context.Context, page, pageSize int) ([]dto.ScheduleVersionResponse, int64, error)
	// Swap and Move adjust the pending draft.
	Swap(ctx context.Context, req *dto.SwapScheduleRequest) (*dto.AdjustmentResponse, error)
	Move(ctx context.Context, req *dto.MoveScheduleRequest) (*dto.AdjustmentResponse, error)
	ListAdjustments(ctx context.Context, page, pageSize int) ([]dto.AdjustmentLogResponse, int64, error)
	ClassroomUtilization(ctx context.Context) ([]dto.ClassroomUtilizationItem, error)
	TeacherWorkload(ctx context.Context) ([]dto.TeacherWorkloadItem, error)
	CourseDensity(ctx context.Context) ([]dto.CourseDensityItem, error)
}

type scheduleService struct {
	schedules   repository.ScheduleRepository
	versions    repository.ScheduleVersionRepository
	classrooms  repository.ClassroomRepository
	teachers    repository.TeacherRepository
	classes     repository.ClassRepository
	courses     repository.CourseRepository
	timeSlots   repository.TimeSlotRepository
	adjustments repository.AdjustmentLogRepository
	uow         repository.UnitOfWork
	logger      *slog.Logger
}

// NewScheduleService constructs a schedule service.
func NewScheduleService(
	schedules repository.ScheduleRepository,
	versions repository.ScheduleVersionRepository,
	classrooms repository.ClassroomRepository,
	teachers repository.TeacherRepository,
	classes repository.ClassRepository,
	courses repository.CourseRepository,
	timeSlots repository.TimeSlotRepository,
	adjustments repository.AdjustmentLogRepository,
	uow repository.UnitOfWork,
	logger *slog.Logger,
) ScheduleService {
	return &scheduleService{
		schedules:   schedules,
		versions:    versions,
		classrooms:  classrooms,
		teachers:    teachers,
		classes:     classes,
		courses:     courses,
		timeSlots:   timeSlots,
		adjustments: adjustments,
		uow:         uow,
		logger:      logger,
	}
}

// Generate creates a timetable with a greedy scheduling algorithm and stores
// it as the pending draft. The official timetable is not modified.
func (s *scheduleService) Generate(ctx context.Context, req *dto.GenerateScheduleRequest) (*dto.GenerateScheduleResponse, error) {
	allSlots, _, err := s.timeSlots.List(ctx, 1, constants.MaxPageSize)
	if err != nil {
		return nil, fmt.Errorf("load time slots: %w", err)
	}
	if req.PeriodsPerDay > len(allSlots) {
		return nil, fmt.Errorf("generate schedule: %w: periods_per_day %d exceeds available time slots %d", ErrInvalid, req.PeriodsPerDay, len(allSlots))
	}
	slots := allSlots[:req.PeriodsPerDay]

	courses, err := s.courses.GetByIDs(ctx, requirementCourseIDs(req.Courses))
	if err != nil {
		return nil, fmt.Errorf("load courses: %w", err)
	}
	courseMap := entityMap(courses, func(c model.Course) uint { return c.ID })

	classes, err := s.resolveClasses(ctx, req)
	if err != nil {
		return nil, err
	}
	teachers, err := s.resolveTeachers(ctx, req)
	if err != nil {
		return nil, err
	}
	classrooms, err := s.resolveClassrooms(ctx, req)
	if err != nil {
		return nil, err
	}
	if len(teachers) == 0 || len(classes) == 0 || len(classrooms) == 0 {
		return nil, fmt.Errorf("generate schedule: %w: teachers, classes and classrooms must not be empty", ErrInvalid)
	}

	teacherCursor := 0
	var allSchedules []model.Schedule
	var conflicts []dto.ConflictResponse
	required := 0

	for week := 1; week <= req.Weeks; week++ {
		occ := newOccupancy()
		for _, requirement := range req.Courses {
			targetClasses := targetClassesForRequirement(classes, requirement, req.ClassIDs)
			for _, class := range targetClasses {
				teacher := pickTeacher(teachers, requirement, courses, courseMap, &teacherCursor)
				if teacher == nil {
					continue
				}
				course, ok := courseMap[requirement.CourseID]
				if !ok {
					conflicts = append(conflicts, dto.ConflictResponse{
						Type: constants.ConflictTeacherTime, EntityType: "course", EntityID: requirement.CourseID,
						EntityName: fmt.Sprintf("course %d", requirement.CourseID), Week: uint(week),
						Suggestion: "course not found",
					})
					continue
				}
				required += requirement.WeeklyPeriods

				positions := buildCandidatePositions(req.DaysPerWeek, slots, requirement.Consecutive, requirement.WeeklyPeriods)
				chosen, chosenClassrooms, ok := placeGreedy(uint(week), positions, requirement.WeeklyPeriods, occ, class, *teacher, course, classrooms, slots)
				if !ok {
					conflicts = append(conflicts, dto.ConflictResponse{
						Type:       constants.ConflictTeacherTime,
						EntityType: "course",
						EntityID:   requirement.CourseID,
						EntityName: course.Name,
						Week:       uint(week),
						Suggestion: fmt.Sprintf("unable to place %d periods for course %s in week %d; add teachers/classrooms or relax constraints", requirement.WeeklyPeriods, course.Name, week),
					})
					continue
				}
				for i := range chosen {
					allSchedules = append(allSchedules, model.Schedule{
						Week:        uint(week),
						DayOfWeek:   chosen[i].Day,
						TimeSlotID:  chosen[i].Slot.ID,
						ClassroomID: chosenClassrooms[i].ID,
						TeacherID:   teacher.ID,
						ClassID:     class.ID,
						CourseID:    course.ID,
						Status:      constants.ScheduleStatusDraft,
					})
				}
			}
		}
	}

	// Regeneration replaces the whole draft; the official timetable and its
	// version history are never touched here.
	err = s.uow.Do(ctx, func(ctx context.Context, repos repository.TxRepositories) error {
		if err := repos.Schedules.DeleteAllByStatus(ctx, constants.ScheduleStatusDraft); err != nil {
			return fmt.Errorf("clear old draft: %w", err)
		}
		if err := repos.Schedules.CreateBatch(ctx, allSchedules); err != nil {
			return fmt.Errorf("persist draft schedules: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	generatedConflicts := s.detectConflicts(ctx, allSchedules)

	responses, err := s.enrichSchedules(ctx, allSchedules)
	if err != nil {
		return nil, fmt.Errorf("enrich schedules: %w", err)
	}
	resp := &dto.GenerateScheduleResponse{
		Schedules: responses,
		Conflicts: append(conflicts, generatedConflicts...),
		Generated: len(allSchedules),
		Required:  required,
	}
	return resp, nil
}

func (s *scheduleService) List(ctx context.Context, week, classID, teacherID, classroomID, versionID *uint) ([]dto.ScheduleResponse, error) {
	filter := repository.ScheduleFilter{
		Week: week, ClassID: classID, TeacherID: teacherID, ClassroomID: classroomID,
		VersionID: versionID,
	}
	if versionID == nil {
		// Queries and exports default to the newest official version per week.
		filter.CurrentOnly = true
	}
	items, err := s.schedules.List(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("list schedules: %w", err)
	}
	return s.enrichSchedules(ctx, items)
}

func (s *scheduleService) ListDraft(ctx context.Context, week *uint) (*dto.DraftResponse, error) {
	status := constants.ScheduleStatusDraft
	items, err := s.schedules.List(ctx, repository.ScheduleFilter{Week: week, Status: &status})
	if err != nil {
		return nil, fmt.Errorf("list draft schedules: %w", err)
	}
	responses, err := s.enrichSchedules(ctx, items)
	if err != nil {
		return nil, err
	}
	return &dto.DraftResponse{
		HasDraft:  len(items) > 0,
		Week:      week,
		Total:     len(items),
		Schedules: responses,
	}, nil
}

func (s *scheduleService) Get(ctx context.Context, id uint) (*dto.ScheduleResponse, error) {
	item, err := s.schedules.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get schedule: %w", err)
	}
	// Only official entries are addressable through this read path.
	if item.Status != constants.ScheduleStatusPublished {
		return nil, ErrNotFound
	}
	responses, err := s.enrichSchedules(ctx, []model.Schedule{*item})
	if err != nil {
		return nil, err
	}
	if len(responses) == 0 {
		return nil, ErrNotFound
	}
	return &responses[0], nil
}

func (s *scheduleService) CheckConflicts(ctx context.Context, week *uint) ([]dto.ConflictResponse, error) {
	items, err := s.conflictSourceEntries(ctx)
	if err != nil {
		return nil, err
	}
	if week != nil {
		filtered := items[:0]
		for _, item := range items {
			if item.Week == *week {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	return s.detectConflicts(ctx, items), nil
}

// conflictSourceEntries returns the draft when one exists, otherwise the
// current official timetable.
func (s *scheduleService) conflictSourceEntries(ctx context.Context) ([]model.Schedule, error) {
	draftStatus := constants.ScheduleStatusDraft
	draftCount, err := s.schedules.CountByStatus(ctx, draftStatus, nil)
	if err != nil {
		return nil, fmt.Errorf("count draft schedules: %w", err)
	}
	if draftCount > 0 {
		items, err := s.schedules.List(ctx, repository.ScheduleFilter{Status: &draftStatus})
		if err != nil {
			return nil, fmt.Errorf("list draft schedules for conflict check: %w", err)
		}
		return items, nil
	}
	items, err := s.schedules.List(ctx, repository.ScheduleFilter{CurrentOnly: true})
	if err != nil {
		return nil, fmt.Errorf("list schedules for conflict check: %w", err)
	}
	return items, nil
}

func (s *scheduleService) Publish(ctx context.Context, req *dto.PublishScheduleRequest, weekFilter *uint) (*dto.PublishScheduleResponse, error) {
	weeks, err := normalizePublishWeeks(req.Weeks, weekFilter)
	if err != nil {
		return nil, err
	}

	draftStatus := constants.ScheduleStatusDraft
	total, err := s.schedules.CountByStatus(ctx, draftStatus, weeks)
	if err != nil {
		return nil, fmt.Errorf("count draft schedules: %w", err)
	}
	if total == 0 {
		return nil, ErrNoDraft
	}

	drafts, err := s.schedules.List(ctx, repository.ScheduleFilter{Status: &draftStatus})
	if err != nil {
		return nil, fmt.Errorf("list draft for publish: %w", err)
	}
	scoped := filterByWeeks(drafts, weeks)
	if conflicts := s.detectConflicts(ctx, scoped); len(conflicts) > 0 {
		return nil, &DraftConflictError{Conflicts: conflicts}
	}

	latest, err := s.versions.Latest(ctx)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return nil, fmt.Errorf("load latest version: %w", err)
	}
	nextVersion := 1
	var previousNote string
	if latest != nil {
		nextVersion = latest.Version + 1
		previousNote = latest.Note
	}

	publishedWeeks := distinctWeeks(scoped)
	version := &model.ScheduleVersion{
		Version:    nextVersion,
		Weeks:      joinWeeks(publishedWeeks),
		WeekCount:  len(publishedWeeks),
		EntryCount: len(scoped),
		Note:       req.Note,
	}
	var promoted []model.Schedule
	err = s.uow.Do(ctx, func(ctx context.Context, repos repository.TxRepositories) error {
		if err := repos.ScheduleVersions.Create(ctx, version); err != nil {
			return fmt.Errorf("create schedule version: %w", err)
		}
		if _, err := repos.Schedules.PromoteDraft(ctx, weeks, version.ID); err != nil {
			return fmt.Errorf("promote draft: %w", err)
		}
		if _, lerr := s.recordAdjustmentWith(ctx, repos.AdjustmentLogs, 0, constants.ActionPublish, map[string]any{
			"version_id": version.ID,
			"version":    version.Version,
			"weeks":      publishedWeeks,
			"published":  len(scoped),
		}); lerr != nil {
			return lerr
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Reload the promoted rows so the response carries their official status.
	publishedStatus := constants.ScheduleStatusPublished
	versionID := version.ID
	published, err := s.schedules.List(ctx, repository.ScheduleFilter{Status: &publishedStatus, VersionID: &versionID})
	if err != nil {
		return nil, fmt.Errorf("list published schedules: %w", err)
	}
	promoted = published
	responses, err := s.enrichSchedules(ctx, promoted)
	if err != nil {
		return nil, fmt.Errorf("enrich published schedules: %w", err)
	}
	return &dto.PublishScheduleResponse{
		VersionID:    version.ID,
		Version:      version.Version,
		Weeks:        publishedWeeks,
		Published:    int64(len(promoted)),
		Schedules:    responses,
		PreviousNote: previousNote,
	}, nil
}

func (s *scheduleService) ListVersions(ctx context.Context, page, pageSize int) ([]dto.ScheduleVersionResponse, int64, error) {
	items, total, err := s.versions.List(ctx, page, pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("list schedule versions: %w", err)
	}
	out := make([]dto.ScheduleVersionResponse, 0, len(items))
	for i := range items {
		out = append(out, dto.ScheduleVersionResponse{
			ID:         items[i].ID,
			Version:    items[i].Version,
			Weeks:      splitWeeks(items[i].Weeks),
			WeekCount:  items[i].WeekCount,
			EntryCount: items[i].EntryCount,
			Note:       items[i].Note,
			CreatedAt:  items[i].CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}
	return out, total, nil
}

// Swap exchanges time and classroom of two entries. Adjustments always land on
// the pending draft; when no draft exists yet, the current official timetable
// is copied into the draft inside the same transaction and the requested
// official entries are mapped to their draft copies. Invalid IDs abort the
// transaction without creating anything.
func (s *scheduleService) Swap(ctx context.Context, req *dto.SwapScheduleRequest) (*dto.AdjustmentResponse, error) {
	var updated *model.Schedule
	var logID uint
	err := s.uow.Do(ctx, func(ctx context.Context, repos repository.TxRepositories) error {
		entries, rerr := s.resolveDraftEntries(ctx, repos, req.ScheduleAID, req.ScheduleBID)
		if rerr != nil {
			return rerr
		}
		a, b := entries[0], entries[1]
		a.Week, b.Week = b.Week, a.Week
		a.DayOfWeek, b.DayOfWeek = b.DayOfWeek, a.DayOfWeek
		a.TimeSlotID, b.TimeSlotID = b.TimeSlotID, a.TimeSlotID
		a.ClassroomID, b.ClassroomID = b.ClassroomID, a.ClassroomID
		if err := repos.Schedules.Update(ctx, a); err != nil {
			return fmt.Errorf("update schedule a: %w", err)
		}
		if err := repos.Schedules.Update(ctx, b); err != nil {
			return fmt.Errorf("update schedule b: %w", err)
		}
		id, lerr := s.recordAdjustmentWith(ctx, repos.AdjustmentLogs, a.ID, constants.ActionSwap, map[string]any{"schedule_a_id": req.ScheduleAID, "schedule_b_id": req.ScheduleBID})
		if lerr != nil {
			return lerr
		}
		logID = id
		updated = a
		return nil
	})
	if err != nil {
		return nil, err
	}
	conflicts, err := s.CheckConflicts(ctx, nil)
	if err != nil {
		return nil, err
	}
	responses, err := s.enrichSchedules(ctx, []model.Schedule{*updated})
	if err != nil {
		return nil, err
	}
	return &dto.AdjustmentResponse{Schedule: responses[0], Conflicts: conflicts, LogID: logID}, nil
}

// Move relocates one entry. See Swap for the draft auto-seeding rule.
func (s *scheduleService) Move(ctx context.Context, req *dto.MoveScheduleRequest) (*dto.AdjustmentResponse, error) {
	var updated *model.Schedule
	var logID uint
	err := s.uow.Do(ctx, func(ctx context.Context, repos repository.TxRepositories) error {
		entries, rerr := s.resolveDraftEntries(ctx, repos, req.ScheduleID)
		if rerr != nil {
			return rerr
		}
		item := entries[0]
		item.Week = req.Week
		item.DayOfWeek = req.DayOfWeek
		item.TimeSlotID = req.TimeSlotID
		item.ClassroomID = req.ClassroomID
		if err := repos.Schedules.Update(ctx, item); err != nil {
			return fmt.Errorf("update schedule: %w", err)
		}
		id, lerr := s.recordAdjustmentWith(ctx, repos.AdjustmentLogs, item.ID, constants.ActionMove, map[string]any{"week": req.Week, "day_of_week": req.DayOfWeek, "time_slot_id": req.TimeSlotID, "classroom_id": req.ClassroomID})
		if lerr != nil {
			return lerr
		}
		logID = id
		updated = item
		return nil
	})
	if err != nil {
		return nil, err
	}
	conflicts, err := s.CheckConflicts(ctx, nil)
	if err != nil {
		return nil, err
	}
	responses, err := s.enrichSchedules(ctx, []model.Schedule{*updated})
	if err != nil {
		return nil, err
	}
	return &dto.AdjustmentResponse{Schedule: responses[0], Conflicts: conflicts, LogID: logID}, nil
}

// resolveDraftEntries returns the draft entries backing the requested IDs.
// When a draft already exists the IDs must reference draft rows directly.
// Otherwise the current official timetable is cloned into a draft and the
// requested IDs must identify current official entries.
func (s *scheduleService) resolveDraftEntries(ctx context.Context, repos repository.TxRepositories, ids ...uint) ([]*model.Schedule, error) {
	draftCount, err := repos.Schedules.CountByStatus(ctx, constants.ScheduleStatusDraft, nil)
	if err != nil {
		return nil, fmt.Errorf("count draft schedules: %w", err)
	}
	if draftCount > 0 {
		entries := make([]*model.Schedule, 0, len(ids))
		for _, id := range ids {
			entry, gerr := repos.Schedules.GetDraftByID(ctx, id)
			if gerr != nil {
				if errors.Is(gerr, repository.ErrNotFound) {
					return nil, ErrNotFound
				}
				return nil, fmt.Errorf("get draft schedule: %w", gerr)
			}
			entries = append(entries, entry)
		}
		return entries, nil
	}

	official, err := repos.Schedules.List(ctx, repository.ScheduleFilter{CurrentOnly: true})
	if err != nil {
		return nil, fmt.Errorf("load official schedules for draft: %w", err)
	}
	byID := map[uint]model.Schedule{}
	for _, item := range official {
		byID[item.ID] = item
	}
	targets := make([]model.Schedule, 0, len(ids))
	for _, id := range ids {
		item, ok := byID[id]
		if !ok {
			return nil, ErrNotFound
		}
		targets = append(targets, item)
	}

	drafts := make([]model.Schedule, 0, len(official))
	for _, item := range official {
		drafts = append(drafts, model.Schedule{
			Week: item.Week, DayOfWeek: item.DayOfWeek, TimeSlotID: item.TimeSlotID,
			ClassroomID: item.ClassroomID, TeacherID: item.TeacherID, ClassID: item.ClassID,
			CourseID: item.CourseID, Status: constants.ScheduleStatusDraft,
		})
	}
	if err := repos.Schedules.CreateBatch(ctx, drafts); err != nil {
		return nil, fmt.Errorf("seed draft from official schedules: %w", err)
	}

	// New draft rows have fresh IDs, so match the requested official rows by
	// their full placement tuple. Buckets tolerate identical duplicate rows.
	buckets := map[string][]*model.Schedule{}
	for i := range drafts {
		key := schedulePlacementKey(drafts[i])
		buckets[key] = append(buckets[key], &drafts[i])
	}
	entries := make([]*model.Schedule, 0, len(ids))
	for _, target := range targets {
		key := schedulePlacementKey(target)
		bucket := buckets[key]
		if len(bucket) == 0 {
			return nil, ErrNotFound
		}
		entries = append(entries, bucket[0])
		buckets[key] = bucket[1:]
	}
	return entries, nil
}

func schedulePlacementKey(item model.Schedule) string {
	return fmt.Sprintf("%d|%d|%d|%d|%d|%d|%d",
		item.Week, item.DayOfWeek, item.TimeSlotID, item.ClassroomID, item.TeacherID, item.ClassID, item.CourseID)
}

func (s *scheduleService) ListAdjustments(ctx context.Context, page, pageSize int) ([]dto.AdjustmentLogResponse, int64, error) {
	items, total, err := s.adjustments.List(ctx, page, pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("list adjustment logs: %w", err)
	}
	out := make([]dto.AdjustmentLogResponse, 0, len(items))
	for i := range items {
		out = append(out, dto.AdjustmentLogResponse{
			ID:         items[i].ID,
			ScheduleID: items[i].ScheduleID,
			Action:     items[i].Action,
			Detail:     items[i].Detail,
			CreatedAt:  items[i].CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}
	return out, total, nil
}

func (s *scheduleService) ClassroomUtilization(ctx context.Context) ([]dto.ClassroomUtilizationItem, error) {
	classrooms, _, err := s.classrooms.List(ctx, 1, constants.MaxPageSize)
	if err != nil {
		return nil, fmt.Errorf("list classrooms: %w", err)
	}
	schedules, err := s.schedules.List(ctx, repository.ScheduleFilter{CurrentOnly: true})
	if err != nil {
		return nil, fmt.Errorf("list schedules: %w", err)
	}
	weeks := distinctWeeks(schedules)
	days := maxDayOfWeek(schedules)
	slots, _, err := s.timeSlots.List(ctx, 1, constants.MaxPageSize)
	if err != nil {
		return nil, fmt.Errorf("list time slots: %w", err)
	}
	totalPeriods := int64(len(weeks)) * int64(days) * int64(len(slots))
	usedByClassroom := map[uint]int64{}
	for _, item := range schedules {
		usedByClassroom[item.ClassroomID]++
	}
	out := make([]dto.ClassroomUtilizationItem, 0, len(classrooms))
	for i := range classrooms {
		used := usedByClassroom[classrooms[i].ID]
		utilization := float64(0)
		if totalPeriods > 0 {
			utilization = float64(used) / float64(totalPeriods)
		}
		out = append(out, dto.ClassroomUtilizationItem{
			ClassroomID:   classrooms[i].ID,
			ClassroomName: classrooms[i].Name,
			UsedPeriods:   used,
			TotalPeriods:  totalPeriods,
			Utilization:   utilization,
		})
	}
	return out, nil
}

func (s *scheduleService) TeacherWorkload(ctx context.Context) ([]dto.TeacherWorkloadItem, error) {
	teachers, _, err := s.teachers.List(ctx, 1, constants.MaxPageSize)
	if err != nil {
		return nil, fmt.Errorf("list teachers: %w", err)
	}
	schedules, err := s.schedules.List(ctx, repository.ScheduleFilter{CurrentOnly: true})
	if err != nil {
		return nil, fmt.Errorf("list schedules: %w", err)
	}
	counts := map[uint]int64{}
	for _, item := range schedules {
		counts[item.TeacherID]++
	}
	out := make([]dto.TeacherWorkloadItem, 0, len(teachers))
	for i := range teachers {
		out = append(out, dto.TeacherWorkloadItem{TeacherID: teachers[i].ID, TeacherName: teachers[i].Name, Periods: counts[teachers[i].ID]})
	}
	return out, nil
}

func (s *scheduleService) CourseDensity(ctx context.Context) ([]dto.CourseDensityItem, error) {
	schedules, err := s.schedules.List(ctx, repository.ScheduleFilter{CurrentOnly: true})
	if err != nil {
		return nil, fmt.Errorf("list schedules: %w", err)
	}
	type key struct {
		day  int
		slot uint
	}
	counts := map[key]int64{}
	for _, item := range schedules {
		k := key{day: item.DayOfWeek, slot: item.TimeSlotID}
		counts[k]++
	}
	out := make([]dto.CourseDensityItem, 0, len(counts))
	for k, v := range counts {
		out = append(out, dto.CourseDensityItem{DayOfWeek: k.day, TimeSlotID: k.slot, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DayOfWeek != out[j].DayOfWeek {
			return out[i].DayOfWeek < out[j].DayOfWeek
		}
		return out[i].TimeSlotID < out[j].TimeSlotID
	})
	return out, nil
}

func (s *scheduleService) recordAdjustment(ctx context.Context, scheduleID uint, action string, detail any) (uint, error) {
	return s.recordAdjustmentWith(ctx, s.adjustments, scheduleID, action, detail)
}

func (s *scheduleService) recordAdjustmentWith(ctx context.Context, repo repository.AdjustmentLogRepository, scheduleID uint, action string, detail any) (uint, error) {
	data, err := json.Marshal(detail)
	if err != nil {
		return 0, fmt.Errorf("marshal adjustment detail: %w", err)
	}
	log := &model.AdjustmentLog{ScheduleID: scheduleID, Action: action, Detail: string(data)}
	if err := repo.Create(ctx, log); err != nil {
		return 0, fmt.Errorf("record adjustment: %w", err)
	}
	return log.ID, nil
}

func normalizePublishWeeks(weeks []uint, weekFilter *uint) ([]uint, error) {
	if len(weeks) > 0 {
		seen := map[uint]bool{}
		out := make([]uint, 0, len(weeks))
		for _, w := range weeks {
			if w == 0 {
				return nil, fmt.Errorf("publish schedule: %w: weeks must be positive", ErrInvalid)
			}
			if !seen[w] {
				seen[w] = true
				out = append(out, w)
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		return out, nil
	}
	if weekFilter != nil {
		return []uint{*weekFilter}, nil
	}
	return nil, nil
}

func filterByWeeks(items []model.Schedule, weeks []uint) []model.Schedule {
	if len(weeks) == 0 {
		return items
	}
	out := make([]model.Schedule, 0, len(items))
	for _, item := range items {
		if containsWeek(weeks, item.Week) {
			out = append(out, item)
		}
	}
	return out
}

func containsWeek(weeks []uint, week uint) bool {
	for _, w := range weeks {
		if w == week {
			return true
		}
	}
	return false
}

func joinWeeks(weeks []uint) string {
	parts := make([]string, 0, len(weeks))
	for _, w := range weeks {
		parts = append(parts, strconv.FormatUint(uint64(w), 10))
	}
	return strings.Join(parts, ",")
}

func splitWeeks(raw string) []uint {
	if strings.TrimSpace(raw) == "" {
		return []uint{}
	}
	parts := strings.Split(raw, ",")
	out := make([]uint, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			continue
		}
		out = append(out, uint(n))
	}
	return out
}

func (s *scheduleService) enrichSchedules(ctx context.Context, items []model.Schedule) ([]dto.ScheduleResponse, error) {
	timeSlots, _, err := s.timeSlots.List(ctx, 1, constants.MaxPageSize)
	if err != nil {
		return nil, fmt.Errorf("load time slots: %w", err)
	}
	slotMap := map[uint]model.TimeSlot{}
	for i := range timeSlots {
		slotMap[timeSlots[i].ID] = timeSlots[i]
	}
	classroomMap, err := s.classroomMap(ctx, items)
	if err != nil {
		return nil, err
	}
	teacherMap, err := s.teacherMap(ctx, items)
	if err != nil {
		return nil, err
	}
	classMap, err := s.classMap(ctx, items)
	if err != nil {
		return nil, err
	}
	courseMap, err := s.courseMap(ctx, items)
	if err != nil {
		return nil, err
	}
	return enrichSchedules(items, slotMap, classroomMap, teacherMap, classMap, courseMap), nil
}

func (s *scheduleService) detectConflicts(ctx context.Context, items []model.Schedule) []dto.ConflictResponse {
	var conflicts []dto.ConflictResponse
	teacherSlots := map[string]model.Schedule{}
	classSlots := map[string]model.Schedule{}
	classroomSlots := map[string]model.Schedule{}

	classes, _ := s.classes.GetByIDs(ctx, uniqueClassIDs(items))
	classroomList, _ := s.classrooms.GetByIDs(ctx, uniqueClassroomIDs(items))
	teacherList, _ := s.teachers.GetByIDs(ctx, uniqueTeacherIDs(items))
	slotList, _, _ := s.timeSlots.List(ctx, 1, constants.MaxPageSize)

	classMap := map[uint]model.Class{}
	for i := range classes {
		classMap[classes[i].ID] = classes[i]
	}
	classroomMap := map[uint]model.Classroom{}
	for i := range classroomList {
		classroomMap[classroomList[i].ID] = classroomList[i]
	}
	teacherMap := map[uint]model.Teacher{}
	for i := range teacherList {
		teacherMap[teacherList[i].ID] = teacherList[i]
	}
	slotMap := map[uint]model.TimeSlot{}
	for i := range slotList {
		slotMap[slotList[i].ID] = slotList[i]
	}

	for _, item := range items {
		slotKey := fmt.Sprintf("%d-%d-%d", item.Week, item.DayOfWeek, item.TimeSlotID)
		if existing, ok := teacherSlots[slotKey+"-t-"+fmt.Sprint(item.TeacherID)]; ok && existing.ID != item.ID {
			conflicts = append(conflicts, dto.ConflictResponse{
				Type: constants.ConflictTeacherTime, EntityType: "teacher", EntityID: item.TeacherID,
				EntityName: teacherMap[item.TeacherID].Name, Week: item.Week, DayOfWeek: item.DayOfWeek, TimeSlotID: item.TimeSlotID,
				Suggestion: fmt.Sprintf("teacher already has a lesson at week %d day %d slot %d; move one of the lessons", item.Week, item.DayOfWeek, item.TimeSlotID),
			})
		}
		if existing, ok := classSlots[slotKey+"-c-"+fmt.Sprint(item.ClassID)]; ok && existing.ID != item.ID {
			conflicts = append(conflicts, dto.ConflictResponse{
				Type: constants.ConflictClassTime, EntityType: "class", EntityID: item.ClassID,
				EntityName: classMap[item.ClassID].Name, Week: item.Week, DayOfWeek: item.DayOfWeek, TimeSlotID: item.TimeSlotID,
				Suggestion: fmt.Sprintf("class already has a lesson at week %d day %d slot %d; move one of the lessons", item.Week, item.DayOfWeek, item.TimeSlotID),
			})
		}
		if existing, ok := classroomSlots[slotKey+"-r-"+fmt.Sprint(item.ClassroomID)]; ok && existing.ID != item.ID {
			conflicts = append(conflicts, dto.ConflictResponse{
				Type: constants.ConflictClassroomTime, EntityType: "classroom", EntityID: item.ClassroomID,
				EntityName: classroomMap[item.ClassroomID].Name, Week: item.Week, DayOfWeek: item.DayOfWeek, TimeSlotID: item.TimeSlotID,
				Suggestion: fmt.Sprintf("classroom already has a lesson at week %d day %d slot %d; move one of the lessons", item.Week, item.DayOfWeek, item.TimeSlotID),
			})
		}
		if class, ok := classMap[item.ClassID]; ok {
			if classroom, ok2 := classroomMap[item.ClassroomID]; ok2 && class.StudentCount > classroom.Capacity {
				conflicts = append(conflicts, dto.ConflictResponse{
					Type: constants.ConflictClassroomCap, EntityType: "class", EntityID: item.ClassID,
					EntityName: class.Name, Week: item.Week, DayOfWeek: item.DayOfWeek, TimeSlotID: item.TimeSlotID,
					Suggestion: fmt.Sprintf("class size %d exceeds classroom capacity %d; choose a larger classroom", class.StudentCount, classroom.Capacity),
				})
			}
		}
		if teacher, ok := teacherMap[item.TeacherID]; ok {
			if slot, ok2 := slotMap[item.TimeSlotID]; ok2 && contains(teacher.UnavailableSlots, slot.Code) {
				conflicts = append(conflicts, dto.ConflictResponse{
					Type: constants.ConflictTeacherPref, EntityType: "teacher", EntityID: item.TeacherID,
					EntityName: teacher.Name, Week: item.Week, DayOfWeek: item.DayOfWeek, TimeSlotID: item.TimeSlotID,
					Suggestion: fmt.Sprintf("slot %s is in the teacher's unavailable periods; choose another time", slot.Code),
				})
			}
		}
		teacherSlots[slotKey+"-t-"+fmt.Sprint(item.TeacherID)] = item
		classSlots[slotKey+"-c-"+fmt.Sprint(item.ClassID)] = item
		classroomSlots[slotKey+"-r-"+fmt.Sprint(item.ClassroomID)] = item
	}
	return conflicts
}
