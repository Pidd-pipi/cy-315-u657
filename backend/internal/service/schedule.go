package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/gbschedule/gbschedule/internal/constants"
	"github.com/gbschedule/gbschedule/internal/dto"
	"github.com/gbschedule/gbschedule/internal/model"
	"github.com/gbschedule/gbschedule/internal/repository"
)

// ScheduleService exposes scheduling, conflict detection, adjustment and statistics operations.
type ScheduleService interface {
	Generate(ctx context.Context, req *dto.GenerateScheduleRequest) (*dto.GenerateScheduleResponse, error)
	List(ctx context.Context, week, classID, teacherID, classroomID, versionID *uint) ([]dto.ScheduleResponse, error)
	Get(ctx context.Context, id uint) (*dto.ScheduleResponse, error)
	CheckConflicts(ctx context.Context) ([]dto.ConflictResponse, error)
	Swap(ctx context.Context, req *dto.SwapScheduleRequest) (*dto.AdjustmentResponse, error)
	Move(ctx context.Context, req *dto.MoveScheduleRequest) (*dto.AdjustmentResponse, error)
	GetDraft(ctx context.Context, week *uint) (*dto.DraftScheduleResponse, error)
	Publish(ctx context.Context, req *dto.PublishScheduleRequest) (*dto.PublishScheduleResponse, error)
	ListVersions(ctx context.Context, page, pageSize int) ([]dto.ScheduleVersionResponse, int64, error)
	GetVersion(ctx context.Context, id uint) (*dto.ScheduleVersionResponse, error)
	ListVersionEntries(ctx context.Context, id uint, week *uint) ([]dto.ScheduleResponse, error)
	ListAdjustments(ctx context.Context, page, pageSize int) ([]dto.AdjustmentLogResponse, int64, error)
	ClassroomUtilization(ctx context.Context) ([]dto.ClassroomUtilizationItem, error)
	TeacherWorkload(ctx context.Context) ([]dto.TeacherWorkloadItem, error)
	CourseDensity(ctx context.Context) ([]dto.CourseDensityItem, error)
}

type scheduleService struct {
	schedules   repository.ScheduleRepository
	drafts      repository.DraftScheduleRepository
	versions    repository.ScheduleVersionRepository
	classrooms  repository.ClassroomRepository
	teachers    repository.TeacherRepository
	classes     repository.ClassRepository
	courses     repository.CourseRepository
	timeSlots   repository.TimeSlotRepository
	adjustments repository.AdjustmentLogRepository
	logger      *slog.Logger
}

// NewScheduleService constructs a schedule service.
func NewScheduleService(
	schedules repository.ScheduleRepository,
	drafts repository.DraftScheduleRepository,
	versions repository.ScheduleVersionRepository,
	classrooms repository.ClassroomRepository,
	teachers repository.TeacherRepository,
	classes repository.ClassRepository,
	courses repository.CourseRepository,
	timeSlots repository.TimeSlotRepository,
	adjustments repository.AdjustmentLogRepository,
	logger *slog.Logger,
) ScheduleService {
	return &scheduleService{
		schedules:   schedules,
		drafts:      drafts,
		versions:    versions,
		classrooms:  classrooms,
		teachers:    teachers,
		classes:     classes,
		courses:     courses,
		timeSlots:   timeSlots,
		adjustments: adjustments,
		logger:      logger,
	}
}

// Generate creates a timetable with a greedy scheduling algorithm.
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
	conflicts := make([]dto.ConflictResponse, 0)
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
				chosen, classrooms, ok := placeGreedy(uint(week), positions, requirement.WeeklyPeriods, occ, class, *teacher, course, classrooms, slots)
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
						ClassroomID: classrooms[i].ID,
						TeacherID:   teacher.ID,
						ClassID:     class.ID,
						CourseID:    course.ID,
					})
				}
			}
		}
	}

	// Regenerate writes to the draft table so an accidental click cannot
	// overwrite the live timetable; publishing the draft is an explicit step.
	draftEntries := make([]model.DraftSchedule, 0, len(allSchedules))
	for _, item := range allSchedules {
		draftEntries = append(draftEntries, draftScheduleFromLive(item, 0))
	}
	if err := s.drafts.DeleteAll(ctx); err != nil {
		return nil, fmt.Errorf("clear old draft schedules: %w", err)
	}
	if err := s.drafts.CreateBatch(ctx, draftEntries); err != nil {
		return nil, fmt.Errorf("persist draft schedules: %w", err)
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
	if versionID != nil {
		entries, err := s.versions.ListEntries(ctx, *versionID, week)
		if err != nil {
			return nil, fmt.Errorf("list version entries: %w", err)
		}
		items := versionEntriesToSchedules(entries)
		if classID != nil || teacherID != nil || classroomID != nil {
			items = filterSchedules(items, classID, teacherID, classroomID)
		}
		return s.enrichSchedules(ctx, items)
	}
	filter := repository.ScheduleFilter{Week: week, ClassID: classID, TeacherID: teacherID, ClassroomID: classroomID}
	items, err := s.schedules.List(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("list schedules: %w", err)
	}
	return s.enrichSchedules(ctx, items)
}

func (s *scheduleService) Get(ctx context.Context, id uint) (*dto.ScheduleResponse, error) {
	item, err := s.schedules.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get schedule: %w", err)
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

func (s *scheduleService) CheckConflicts(ctx context.Context) ([]dto.ConflictResponse, error) {
	items, err := s.schedules.List(ctx, repository.ScheduleFilter{})
	if err != nil {
		return nil, fmt.Errorf("list schedules for conflict check: %w", err)
	}
	return s.detectConflicts(ctx, items), nil
}

// ensureDraft returns the draft entries, seeding them from the live timetable
// when no draft exists yet. Swap/move never mutate the live timetable directly.
func (s *scheduleService) ensureDraft(ctx context.Context) ([]model.DraftSchedule, error) {
	items, err := s.drafts.List(ctx, repository.DraftScheduleFilter{})
	if err != nil {
		return nil, fmt.Errorf("list draft schedules: %w", err)
	}
	if len(items) > 0 {
		return items, nil
	}
	live, err := s.schedules.List(ctx, repository.ScheduleFilter{})
	if err != nil {
		return nil, fmt.Errorf("list live schedules: %w", err)
	}
	drafts := make([]model.DraftSchedule, 0, len(live))
	for _, item := range live {
		drafts = append(drafts, draftScheduleFromLive(item, item.ID))
	}
	if err := s.drafts.CreateBatch(ctx, drafts); err != nil {
		return nil, fmt.Errorf("seed draft schedules: %w", err)
	}
	return drafts, nil
}

func (s *scheduleService) findDraftByRef(drafts []model.DraftSchedule, ref uint) (int, bool) {
	for i := range drafts {
		if drafts[i].ID == ref || drafts[i].SourceScheduleID == ref {
			return i, true
		}
	}
	return 0, false
}

func (s *scheduleService) Swap(ctx context.Context, req *dto.SwapScheduleRequest) (*dto.AdjustmentResponse, error) {
	drafts, err := s.ensureDraft(ctx)
	if err != nil {
		return nil, err
	}
	ai, ok := s.findDraftByRef(drafts, req.ScheduleAID)
	if !ok {
		return nil, ErrNotFound
	}
	bi, ok := s.findDraftByRef(drafts, req.ScheduleBID)
	if !ok {
		return nil, ErrNotFound
	}
	a, b := &drafts[ai], &drafts[bi]
	a.Week, b.Week = b.Week, a.Week
	a.DayOfWeek, b.DayOfWeek = b.DayOfWeek, a.DayOfWeek
	a.TimeSlotID, b.TimeSlotID = b.TimeSlotID, a.TimeSlotID
	a.ClassroomID, b.ClassroomID = b.ClassroomID, a.ClassroomID
	if err := s.drafts.Update(ctx, a); err != nil {
		return nil, fmt.Errorf("update draft schedule a: %w", err)
	}
	if err := s.drafts.Update(ctx, b); err != nil {
		return nil, fmt.Errorf("update draft schedule b: %w", err)
	}
	logID, err := s.recordAdjustment(ctx, a.ID, constants.ActionSwap, map[string]any{"schedule_a_id": req.ScheduleAID, "schedule_b_id": req.ScheduleBID})
	if err != nil {
		return nil, err
	}
	return s.adjustmentResult(ctx, a, logID)
}

func (s *scheduleService) Move(ctx context.Context, req *dto.MoveScheduleRequest) (*dto.AdjustmentResponse, error) {
	drafts, err := s.ensureDraft(ctx)
	if err != nil {
		return nil, err
	}
	idx, ok := s.findDraftByRef(drafts, req.ScheduleID)
	if !ok {
		return nil, ErrNotFound
	}
	item := &drafts[idx]
	item.Week = req.Week
	item.DayOfWeek = req.DayOfWeek
	item.TimeSlotID = req.TimeSlotID
	item.ClassroomID = req.ClassroomID
	if err := s.drafts.Update(ctx, item); err != nil {
		return nil, fmt.Errorf("update draft schedule: %w", err)
	}
	logID, err := s.recordAdjustment(ctx, item.ID, constants.ActionMove, map[string]any{"week": req.Week, "day_of_week": req.DayOfWeek, "time_slot_id": req.TimeSlotID, "classroom_id": req.ClassroomID})
	if err != nil {
		return nil, err
	}
	return s.adjustmentResult(ctx, item, logID)
}

func (s *scheduleService) adjustmentResult(ctx context.Context, item *model.DraftSchedule, logID uint) (*dto.AdjustmentResponse, error) {
	live := draftToSchedule(*item)
	responses, err := s.enrichSchedules(ctx, []model.Schedule{live})
	if err != nil {
		return nil, err
	}
	drafts, err := s.drafts.List(ctx, repository.DraftScheduleFilter{})
	if err != nil {
		return nil, fmt.Errorf("list draft schedules for conflict check: %w", err)
	}
	conflicts := s.detectConflicts(ctx, draftsToSchedules(drafts))
	return &dto.AdjustmentResponse{Schedule: responses[0], Conflicts: conflicts, LogID: logID}, nil
}

// GetDraft returns the unpublished draft timetable, optionally filtered by week.
func (s *scheduleService) GetDraft(ctx context.Context, week *uint) (*dto.DraftScheduleResponse, error) {
	items, err := s.drafts.List(ctx, repository.DraftScheduleFilter{Week: week})
	if err != nil {
		return nil, fmt.Errorf("list draft schedules: %w", err)
	}
	if len(items) == 0 {
		return &dto.DraftScheduleResponse{Exists: false, Total: 0, Weeks: []uint{}, Schedules: []dto.ScheduleResponse{}, Conflicts: []dto.ConflictResponse{}}, nil
	}
	schedules := draftsToSchedules(items)
	responses, err := s.enrichSchedules(ctx, schedules)
	if err != nil {
		return nil, fmt.Errorf("enrich draft schedules: %w", err)
	}
	conflicts := s.detectConflicts(ctx, schedules)
	return &dto.DraftScheduleResponse{
		Exists:    true,
		Total:     len(items),
		Weeks:     distinctWeeks(schedules),
		Schedules: responses,
		Conflicts: conflicts,
	}, nil
}

// Publish validates the draft for the requested weeks and atomically promotes
// it into the live timetable, archiving the previous live timetable first.
func (s *scheduleService) Publish(ctx context.Context, req *dto.PublishScheduleRequest) (*dto.PublishScheduleResponse, error) {
	weeks, err := s.resolvePublishWeeks(ctx, req.Weeks)
	if err != nil {
		return nil, err
	}
	drafts, err := s.drafts.List(ctx, repository.DraftScheduleFilter{})
	if err != nil {
		return nil, fmt.Errorf("list draft schedules: %w", err)
	}
	if len(drafts) == 0 {
		return nil, ErrNoDraft
	}

	// Only conflicts within the published weeks block promotion; unrelated
	// draft weeks are out of scope for this publish.
	allConflicts := s.detectConflicts(ctx, draftsToSchedules(drafts))
	conflicts := filterConflictsByWeeks(allConflicts, weeks)
	if len(conflicts) > 0 {
		return nil, &ConflictListError{Conflicts: conflicts}
	}

	version, err := s.versions.Publish(ctx, weeks, time.Now(), req.Note)
	if err != nil {
		if errors.Is(err, repository.ErrNoDraft) {
			return nil, ErrNoDraft
		}
		return nil, fmt.Errorf("publish draft: %w", err)
	}

	filterWeek := uint(0)
	if len(weeks) == 1 {
		filterWeek = weeks[0]
	}
	entries, err := s.versions.ListEntries(ctx, version.ID, nil)
	if err != nil {
		return nil, fmt.Errorf("load published version: %w", err)
	}
	liveItems := versionEntriesToSchedules(entries)
	if filterWeek > 0 {
		filtered := make([]model.Schedule, 0)
		for _, item := range liveItems {
			if item.Week == filterWeek {
				filtered = append(filtered, item)
			}
		}
		liveItems = filtered
	}
	responses, err := s.enrichSchedules(ctx, liveItems)
	if err != nil {
		return nil, fmt.Errorf("enrich published schedules: %w", err)
	}

	publishedCount := 0
	weekSet := make(map[uint]bool, len(weeks))
	for _, w := range weeks {
		weekSet[w] = true
	}
	for _, d := range drafts {
		if weekSet[d.Week] {
			publishedCount++
		}
	}

	if _, err := s.recordAdjustment(ctx, 0, constants.ActionPublish, map[string]any{
		"version_id":      version.ID,
		"version":         version.Version,
		"weeks":           weeks,
		"published_count": publishedCount,
	}); err != nil {
		return nil, err
	}

	return &dto.PublishScheduleResponse{
		VersionID:      version.ID,
		Version:        version.Version,
		PublishedWeeks: weeks,
		PublishedCount: publishedCount,
		LiveCount:      len(entries),
		PublishedAt:    version.PublishedAt.Format("2006-01-02 15:04:05"),
		Schedules:      responses,
	}, nil
}

func (s *scheduleService) resolvePublishWeeks(ctx context.Context, requested []uint) ([]uint, error) {
	all, err := s.drafts.List(ctx, repository.DraftScheduleFilter{})
	if err != nil {
		return nil, fmt.Errorf("list draft weeks: %w", err)
	}
	draftWeeks := distinctWeeks(draftsToSchedules(all))
	if len(requested) == 0 {
		return draftWeeks, nil
	}
	available := map[uint]bool{}
	for _, w := range draftWeeks {
		available[w] = true
	}
	seen := map[uint]bool{}
	weeks := make([]uint, 0, len(requested))
	for _, w := range requested {
		if !available[w] {
			return nil, fmt.Errorf("publish schedule: %w: no draft entries for week %d", ErrInvalid, w)
		}
		if !seen[w] {
			seen[w] = true
			weeks = append(weeks, w)
		}
	}
	sort.Slice(weeks, func(i, j int) bool { return weeks[i] < weeks[j] })
	return weeks, nil
}

func (s *scheduleService) ListVersions(ctx context.Context, page, pageSize int) ([]dto.ScheduleVersionResponse, int64, error) {
	items, total, err := s.versions.List(ctx, page, pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("list schedule versions: %w", err)
	}
	out := make([]dto.ScheduleVersionResponse, 0, len(items))
	for i := range items {
		out = append(out, versionResponse(items[i]))
	}
	return out, total, nil
}

func (s *scheduleService) GetVersion(ctx context.Context, id uint) (*dto.ScheduleVersionResponse, error) {
	item, err := s.versions.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get schedule version: %w", err)
	}
	resp := versionResponse(*item)
	return &resp, nil
}

func (s *scheduleService) ListVersionEntries(ctx context.Context, id uint, week *uint) ([]dto.ScheduleResponse, error) {
	if _, err := s.versions.GetByID(ctx, id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get schedule version: %w", err)
	}
	entries, err := s.versions.ListEntries(ctx, id, week)
	if err != nil {
		return nil, fmt.Errorf("list version entries: %w", err)
	}
	return s.enrichSchedules(ctx, versionEntriesToSchedules(entries))
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
	schedules, err := s.schedules.List(ctx, repository.ScheduleFilter{})
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
	schedules, err := s.schedules.List(ctx, repository.ScheduleFilter{})
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
	schedules, err := s.schedules.List(ctx, repository.ScheduleFilter{})
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
	data, err := json.Marshal(detail)
	if err != nil {
		return 0, fmt.Errorf("marshal adjustment detail: %w", err)
	}
	log := &model.AdjustmentLog{ScheduleID: scheduleID, Action: action, Detail: string(data)}
	if err := s.adjustments.Create(ctx, log); err != nil {
		return 0, fmt.Errorf("record adjustment: %w", err)
	}
	return log.ID, nil
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
	conflicts := make([]dto.ConflictResponse, 0)
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
