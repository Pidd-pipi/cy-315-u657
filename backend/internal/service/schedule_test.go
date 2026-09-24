package service_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/gbschedule/gbschedule/internal/constants"
	"github.com/gbschedule/gbschedule/internal/dto"
	"github.com/gbschedule/gbschedule/internal/model"
	"github.com/gbschedule/gbschedule/internal/repository"
	"github.com/gbschedule/gbschedule/internal/service"
)

func newScheduleTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Classroom{}, &model.Teacher{}, &model.Class{}, &model.Course{}, &model.TimeSlot{}, &model.Schedule{}, &model.DraftSchedule{}, &model.ScheduleVersion{}, &model.ScheduleVersionEntry{}, &model.AdjustmentLog{}); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	return db
}

func newScheduleService(t *testing.T, db *gorm.DB) service.ScheduleService {
	logger := slog.New(slog.NewTextHandler(&strings.Builder{}, nil))
	return service.NewScheduleService(
		repository.NewScheduleRepository(db),
		repository.NewDraftScheduleRepository(db),
		repository.NewScheduleVersionRepository(db),
		repository.NewClassroomRepository(db),
		repository.NewTeacherRepository(db),
		repository.NewClassRepository(db),
		repository.NewCourseRepository(db),
		repository.NewTimeSlotRepository(db),
		repository.NewAdjustmentLogRepository(db),
		logger,
	)
}

func TestScheduleServiceDetectConflicts(t *testing.T) {
	ctx := context.Background()
	db := newScheduleTestDB(t)

	slot1 := &model.TimeSlot{Code: "1", Name: "第一节", StartTime: "08:00", EndTime: "09:40"}
	slot2 := &model.TimeSlot{Code: "2", Name: "第二节", StartTime: "10:00", EndTime: "11:40"}
	if err := db.Create(slot1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(slot2).Error; err != nil {
		t.Fatal(err)
	}

	smallRoom := &model.Classroom{Code: "R301", Name: "小教室", Capacity: 30}
	bigRoom := &model.Classroom{Code: "R302", Name: "大教室", Capacity: 50}
	if err := db.Create(smallRoom).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(bigRoom).Error; err != nil {
		t.Fatal(err)
	}

	teacher := &model.Teacher{Name: "张老师", EmployeeNo: "T001", Subjects: []string{"数学"}, UnavailableSlots: []string{"1"}}
	if err := db.Create(teacher).Error; err != nil {
		t.Fatal(err)
	}
	class := &model.Class{Name: "一班", StudentCount: 40, Grade: "高一"}
	if err := db.Create(class).Error; err != nil {
		t.Fatal(err)
	}
	course := &model.Course{Name: "数学", Code: "MATH", Duration: 1}
	if err := db.Create(course).Error; err != nil {
		t.Fatal(err)
	}

	schedules := []model.Schedule{
		{Week: 1, DayOfWeek: 1, TimeSlotID: slot1.ID, ClassroomID: smallRoom.ID, TeacherID: teacher.ID, ClassID: class.ID, CourseID: course.ID},
		{Week: 1, DayOfWeek: 1, TimeSlotID: slot1.ID, ClassroomID: bigRoom.ID, TeacherID: teacher.ID, ClassID: class.ID, CourseID: course.ID},
	}
	if err := db.Create(&schedules).Error; err != nil {
		t.Fatal(err)
	}

	svc := newScheduleService(t, db)
	conflicts, err := svc.CheckConflicts(ctx)
	if err != nil {
		t.Fatalf("check conflicts: %v", err)
	}

	wantTypes := []string{
		constants.ConflictTeacherTime,
		constants.ConflictClassTime,
		constants.ConflictClassroomCap,
		constants.ConflictTeacherPref,
	}
	gotTypes := map[string]bool{}
	for _, c := range conflicts {
		gotTypes[c.Type] = true
	}
	for _, want := range wantTypes {
		if !gotTypes[want] {
			t.Errorf("missing conflict type %s in %v", want, conflicts)
		}
	}
}

func TestScheduleServiceGenerateAllowsParallelClasses(t *testing.T) {
	ctx := context.Background()
	db := newScheduleTestDB(t)

	slot := &model.TimeSlot{Code: "1", Name: "第一节", StartTime: "08:00", EndTime: "09:40"}
	if err := db.Create(slot).Error; err != nil {
		t.Fatal(err)
	}
	room1 := &model.Classroom{Code: "R301", Name: "301教室", Capacity: 50}
	room2 := &model.Classroom{Code: "R302", Name: "302教室", Capacity: 50}
	if err := db.Create(room1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(room2).Error; err != nil {
		t.Fatal(err)
	}
	teacher1 := &model.Teacher{Name: "张老师", EmployeeNo: "T001", Subjects: []string{"数学"}}
	teacher2 := &model.Teacher{Name: "李老师", EmployeeNo: "T002", Subjects: []string{"语文"}}
	if err := db.Create(teacher1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(teacher2).Error; err != nil {
		t.Fatal(err)
	}
	class1 := &model.Class{Name: "一班", StudentCount: 40, Grade: "高一"}
	class2 := &model.Class{Name: "二班", StudentCount: 40, Grade: "高一"}
	if err := db.Create(class1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(class2).Error; err != nil {
		t.Fatal(err)
	}
	course1 := &model.Course{Name: "数学", Code: "MATH", Duration: 1}
	course2 := &model.Course{Name: "语文", Code: "CHN", Duration: 1}
	if err := db.Create(course1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(course2).Error; err != nil {
		t.Fatal(err)
	}

	svc := newScheduleService(t, db)
	resp, err := svc.Generate(ctx, &dto.GenerateScheduleRequest{
		Weeks:         1,
		DaysPerWeek:   1,
		PeriodsPerDay: 1,
		Courses: []dto.CourseRequirement{
			{CourseID: course1.ID, WeeklyPeriods: 1, ClassID: class1.ID, TeacherID: teacher1.ID},
			{CourseID: course2.ID, WeeklyPeriods: 1, ClassID: class2.ID, TeacherID: teacher2.ID},
		},
		TeacherIDs:   []uint{teacher1.ID, teacher2.ID},
		ClassIDs:     []uint{class1.ID, class2.ID},
		ClassroomIDs: []uint{room1.ID, room2.ID},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if resp.Generated != 2 || len(resp.Schedules) != 2 {
		t.Fatalf("expected 2 generated schedules, got generated=%d schedules=%d", resp.Generated, len(resp.Schedules))
	}
	if len(resp.Conflicts) != 0 {
		t.Fatalf("expected no conflicts, got %+v", resp.Conflicts)
	}
}

func TestScheduleServiceGenerateReplacesStaleWeeks(t *testing.T) {
	ctx := context.Background()
	db := newScheduleTestDB(t)

	slot := &model.TimeSlot{Code: "1", Name: "第一节", StartTime: "08:00", EndTime: "09:40"}
	if err := db.Create(slot).Error; err != nil {
		t.Fatal(err)
	}
	room := &model.Classroom{Code: "R301", Name: "301教室", Capacity: 50}
	if err := db.Create(room).Error; err != nil {
		t.Fatal(err)
	}
	teacher := &model.Teacher{Name: "张老师", EmployeeNo: "T001", Subjects: []string{"数学"}}
	if err := db.Create(teacher).Error; err != nil {
		t.Fatal(err)
	}
	class := &model.Class{Name: "一班", StudentCount: 40, Grade: "高一"}
	if err := db.Create(class).Error; err != nil {
		t.Fatal(err)
	}
	course := &model.Course{Name: "数学", Code: "MATH", Duration: 1}
	if err := db.Create(course).Error; err != nil {
		t.Fatal(err)
	}

	svc := newScheduleService(t, db)
	req := func(weeks int) *dto.GenerateScheduleRequest {
		return &dto.GenerateScheduleRequest{
			Weeks:         weeks,
			DaysPerWeek:   1,
			PeriodsPerDay: 1,
			Courses: []dto.CourseRequirement{
				{CourseID: course.ID, WeeklyPeriods: 1, ClassID: class.ID, TeacherID: teacher.ID},
			},
			TeacherIDs:   []uint{teacher.ID},
			ClassIDs:     []uint{class.ID},
			ClassroomIDs: []uint{room.ID},
		}
	}

	if _, err := svc.Generate(ctx, req(2)); err != nil {
		t.Fatalf("generate 2 weeks: %v", err)
	}
	if _, err := svc.Generate(ctx, req(1)); err != nil {
		t.Fatalf("generate 1 week: %v", err)
	}
	draft, err := svc.GetDraft(ctx, nil)
	if err != nil {
		t.Fatalf("get draft: %v", err)
	}
	if !draft.Exists || draft.Total != 1 {
		t.Fatalf("expected 1 draft entry after regeneration, got exists=%v total=%d", draft.Exists, draft.Total)
	}
	if len(draft.Schedules) != 1 || draft.Schedules[0].Week != 1 {
		t.Fatalf("expected only week 1 to remain in draft, got %+v", draft.Schedules)
	}
	// The live timetable must stay untouched until publish.
	live, err := svc.List(ctx, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("list live schedules: %v", err)
	}
	if len(live) != 0 {
		t.Fatalf("expected live timetable to be untouched before publish, got %d entries", len(live))
	}
}

func seedDraftSchedule(t *testing.T, db *gorm.DB, week uint, day int, slotID, roomID, teacherID, classID, courseID uint, sourceID uint) model.DraftSchedule {
	t.Helper()
	draft := model.DraftSchedule{
		Week: week, DayOfWeek: day, TimeSlotID: slotID, ClassroomID: roomID,
		TeacherID: teacherID, ClassID: classID, CourseID: courseID, SourceScheduleID: sourceID,
	}
	if err := db.Create(&draft).Error; err != nil {
		t.Fatalf("create draft schedule: %v", err)
	}
	return draft
}

func TestScheduleServiceDraftPublishAndVersionHistory(t *testing.T) {
	ctx := context.Background()
	db := newScheduleTestDB(t)

	slot := &model.TimeSlot{Code: "1", Name: "第一节", StartTime: "08:00", EndTime: "09:40"}
	if err := db.Create(slot).Error; err != nil {
		t.Fatal(err)
	}
	room := &model.Classroom{Code: "R301", Name: "301教室", Capacity: 50}
	if err := db.Create(room).Error; err != nil {
		t.Fatal(err)
	}
	teacher := &model.Teacher{Name: "张老师", EmployeeNo: "T001", Subjects: []string{"数学"}}
	if err := db.Create(teacher).Error; err != nil {
		t.Fatal(err)
	}
	class := &model.Class{Name: "一班", StudentCount: 40, Grade: "高一"}
	if err := db.Create(class).Error; err != nil {
		t.Fatal(err)
	}
	course := &model.Course{Name: "数学", Code: "MATH", Duration: 1}
	if err := db.Create(course).Error; err != nil {
		t.Fatal(err)
	}

	svc := newScheduleService(t, db)

	// Week-filtered draft view before anything exists.
	empty, err := svc.GetDraft(ctx, ptrUint(1))
	if err != nil {
		t.Fatalf("get empty draft: %v", err)
	}
	if empty.Exists || empty.Total != 0 {
		t.Fatalf("expected no draft, got %+v", empty)
	}

	// First publish: draft has weeks 1 and 2.
	seedDraftSchedule(t, db, 1, 1, slot.ID, room.ID, teacher.ID, class.ID, course.ID, 0)
	seedDraftSchedule(t, db, 2, 1, slot.ID, room.ID, teacher.ID, class.ID, course.ID, 0)

	resp, err := svc.Publish(ctx, &dto.PublishScheduleRequest{Note: "first publish"})
	if err != nil {
		t.Fatalf("publish all weeks: %v", err)
	}
	if resp.Version != 1 || resp.PublishedCount != 2 || resp.LiveCount != 2 {
		t.Fatalf("unexpected publish response: %+v", resp)
	}
	if len(resp.PublishedWeeks) != 2 || resp.PublishedWeeks[0] != 1 || resp.PublishedWeeks[1] != 2 {
		t.Fatalf("unexpected published weeks: %+v", resp.PublishedWeeks)
	}

	live, err := svc.List(ctx, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("list live: %v", err)
	}
	if len(live) != 2 {
		t.Fatalf("expected 2 live entries, got %d", len(live))
	}
	week1, err := svc.List(ctx, ptrUint(1), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("list live week 1: %v", err)
	}
	if len(week1) != 1 || week1[0].Week != 1 {
		t.Fatalf("expected 1 live entry in week 1, got %+v", week1)
	}

	// Draft is consumed after publish.
	draft, err := svc.GetDraft(ctx, nil)
	if err != nil {
		t.Fatalf("get draft: %v", err)
	}
	if draft.Exists {
		t.Fatalf("expected draft to be cleared after publish, got %d entries", draft.Total)
	}

	// Second publish for week 2 only archives v1 and merges into v2.
	seedDraftSchedule(t, db, 2, 2, slot.ID, room.ID, teacher.ID, class.ID, course.ID, 0)
	resp2, err := svc.Publish(ctx, &dto.PublishScheduleRequest{Weeks: []uint{2}})
	if err != nil {
		t.Fatalf("publish week 2: %v", err)
	}
	if resp2.Version != 2 || resp2.PublishedCount != 1 || resp2.LiveCount != 2 {
		t.Fatalf("unexpected second publish response: %+v", resp2)
	}
	live2, err := svc.List(ctx, ptrUint(2), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("list live week 2: %v", err)
	}
	if len(live2) != 1 || live2[0].DayOfWeek != 2 {
		t.Fatalf("expected week 2 replaced entry on day 2, got %+v", live2)
	}

	// Version history: v1 archived, v2 current; entries remain queryable.
	versions, total, err := svc.ListVersions(ctx, 1, 20)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if total != 2 || len(versions) != 2 {
		t.Fatalf("expected 2 versions, got total=%d items=%d", total, len(versions))
	}
	if versions[0].Version != 2 || versions[0].Status != constants.VersionCurrent {
		t.Fatalf("expected newest version 2 current, got %+v", versions[0])
	}
	if versions[1].Version != 1 || versions[1].Status != constants.VersionArchived {
		t.Fatalf("expected version 1 archived, got %+v", versions[1])
	}
	v1Entries, err := svc.ListVersionEntries(ctx, versions[1].ID, nil)
	if err != nil {
		t.Fatalf("list v1 entries: %v", err)
	}
	if len(v1Entries) != 2 {
		t.Fatalf("expected 2 entries archived in v1, got %d", len(v1Entries))
	}
	v1Week2, err := svc.ListVersionEntries(ctx, versions[1].ID, ptrUint(2))
	if err != nil {
		t.Fatalf("list v1 week 2 entries: %v", err)
	}
	if len(v1Week2) != 1 || v1Week2[0].DayOfWeek != 1 {
		t.Fatalf("expected archived week 2 entry on day 1, got %+v", v1Week2)
	}
}

func TestScheduleServicePublishConflictBlocksAndReports(t *testing.T) {
	ctx := context.Background()
	db := newScheduleTestDB(t)

	slot := &model.TimeSlot{Code: "1", Name: "第一节", StartTime: "08:00", EndTime: "09:40"}
	if err := db.Create(slot).Error; err != nil {
		t.Fatal(err)
	}
	room1 := &model.Classroom{Code: "R301", Name: "301教室", Capacity: 50}
	room2 := &model.Classroom{Code: "R302", Name: "302教室", Capacity: 50}
	if err := db.Create(room1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(room2).Error; err != nil {
		t.Fatal(err)
	}
	teacher1 := &model.Teacher{Name: "张老师", EmployeeNo: "T001", Subjects: []string{"数学"}}
	teacher2 := &model.Teacher{Name: "李老师", EmployeeNo: "T002", Subjects: []string{"语文"}}
	if err := db.Create(teacher1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(teacher2).Error; err != nil {
		t.Fatal(err)
	}
	class1 := &model.Class{Name: "一班", StudentCount: 40}
	class2 := &model.Class{Name: "二班", StudentCount: 40}
	if err := db.Create(class1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(class2).Error; err != nil {
		t.Fatal(err)
	}
	course1 := &model.Course{Name: "数学", Code: "MATH", Duration: 1}
	course2 := &model.Course{Name: "语文", Code: "CHN", Duration: 1}
	if err := db.Create(course1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(course2).Error; err != nil {
		t.Fatal(err)
	}

	svc := newScheduleService(t, db)

	// Publish a clean baseline first.
	seedDraftSchedule(t, db, 1, 1, slot.ID, room1.ID, teacher1.ID, class1.ID, course1.ID, 0)
	seedDraftSchedule(t, db, 1, 1, slot.ID, room2.ID, teacher2.ID, class2.ID, course2.ID, 0)
	if _, err := svc.Publish(ctx, &dto.PublishScheduleRequest{}); err != nil {
		t.Fatalf("baseline publish: %v", err)
	}

	// Move on the seeded draft creates a teacher/classroom clash.
	live, err := svc.List(ctx, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("list live: %v", err)
	}
	moveReq := &dto.MoveScheduleRequest{
		ScheduleID:  live[0].ID,
		Week:        1,
		DayOfWeek:   1,
		TimeSlotID:  slot.ID,
		ClassroomID: room2.ID,
	}
	adjustment, err := svc.Move(ctx, moveReq)
	if err != nil {
		t.Fatalf("move to draft: %v", err)
	}
	if len(adjustment.Conflicts) == 0 {
		t.Fatalf("expected draft conflicts after move, got none")
	}

	// Live timetable must remain unchanged while the conflicting draft exists.
	liveAfterMove, err := svc.List(ctx, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("list live after move: %v", err)
	}
	if len(liveAfterMove) != 2 || liveAfterMove[0].ClassroomID != room1.ID {
		t.Fatalf("live timetable changed by draft move: %+v", liveAfterMove)
	}

	_, err = svc.Publish(ctx, &dto.PublishScheduleRequest{})
	if err == nil {
		t.Fatalf("expected publish to fail on conflicts")
	}
	var conflictErr *service.ConflictListError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("expected ConflictListError, got %T: %v", err, err)
	}
	if len(conflictErr.Conflicts) == 0 {
		t.Fatalf("expected conflict list in error")
	}
	types := map[string]bool{}
	for _, c := range conflictErr.Conflicts {
		types[c.Type] = true
	}
	if !types[constants.ConflictClassroomTime] {
		t.Fatalf("expected classroom time conflict, got %+v", conflictErr.Conflicts)
	}

	// No version may be created on a blocked publish.
	_, total, err := svc.ListVersions(ctx, 1, 20)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected only the baseline version, got %d", total)
	}
}

func TestScheduleServicePublishRejectsUnknownWeek(t *testing.T) {
	ctx := context.Background()
	db := newScheduleTestDB(t)

	slot := &model.TimeSlot{Code: "1", Name: "第一节", StartTime: "08:00", EndTime: "09:40"}
	if err := db.Create(slot).Error; err != nil {
		t.Fatal(err)
	}
	room := &model.Classroom{Code: "R301", Name: "301教室", Capacity: 50}
	if err := db.Create(room).Error; err != nil {
		t.Fatal(err)
	}
	teacher := &model.Teacher{Name: "张老师", EmployeeNo: "T001"}
	if err := db.Create(teacher).Error; err != nil {
		t.Fatal(err)
	}
	class := &model.Class{Name: "一班", StudentCount: 40}
	if err := db.Create(class).Error; err != nil {
		t.Fatal(err)
	}
	course := &model.Course{Name: "数学", Code: "MATH", Duration: 1}
	if err := db.Create(course).Error; err != nil {
		t.Fatal(err)
	}

	svc := newScheduleService(t, db)
	seedDraftSchedule(t, db, 1, 1, slot.ID, room.ID, teacher.ID, class.ID, course.ID, 0)

	if _, err := svc.Publish(ctx, &dto.PublishScheduleRequest{Weeks: []uint{3}}); !errors.Is(err, service.ErrInvalid) {
		t.Fatalf("expected ErrInvalid for week without draft, got %v", err)
	}
}

func TestScheduleServiceFirstPublishArchivesLegacyLiveTimetable(t *testing.T) {
	ctx := context.Background()
	db := newScheduleTestDB(t)

	slot := &model.TimeSlot{Code: "1", Name: "第一节", StartTime: "08:00", EndTime: "09:40"}
	if err := db.Create(slot).Error; err != nil {
		t.Fatal(err)
	}
	room := &model.Classroom{Code: "R301", Name: "301教室", Capacity: 50}
	if err := db.Create(room).Error; err != nil {
		t.Fatal(err)
	}
	teacher := &model.Teacher{Name: "张老师", EmployeeNo: "T001"}
	if err := db.Create(teacher).Error; err != nil {
		t.Fatal(err)
	}
	class := &model.Class{Name: "一班", StudentCount: 40}
	if err := db.Create(class).Error; err != nil {
		t.Fatal(err)
	}
	course := &model.Course{Name: "数学", Code: "MATH", Duration: 1}
	if err := db.Create(course).Error; err != nil {
		t.Fatal(err)
	}

	// Simulate a live timetable created before versioning existed.
	legacy := model.Schedule{Week: 1, DayOfWeek: 1, TimeSlotID: slot.ID, ClassroomID: room.ID, TeacherID: teacher.ID, ClassID: class.ID, CourseID: course.ID}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}

	svc := newScheduleService(t, db)
	seedDraftSchedule(t, db, 1, 2, slot.ID, room.ID, teacher.ID, class.ID, course.ID, 0)

	resp, err := svc.Publish(ctx, &dto.PublishScheduleRequest{})
	if err != nil {
		t.Fatalf("publish over legacy live: %v", err)
	}
	if resp.Version != 2 {
		t.Fatalf("expected draft published as version 2, got %d", resp.Version)
	}

	versions, total, err := svc.ListVersions(ctx, 1, 20)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if total != 2 || versions[1].Version != 1 || versions[1].Status != constants.VersionArchived {
		t.Fatalf("expected legacy timetable archived as v1, got %+v", versions)
	}
	legacyEntries, err := svc.ListVersionEntries(ctx, versions[1].ID, nil)
	if err != nil {
		t.Fatalf("list legacy entries: %v", err)
	}
	if len(legacyEntries) != 1 || legacyEntries[0].DayOfWeek != 1 {
		t.Fatalf("expected archived legacy entry on day 1, got %+v", legacyEntries)
	}
}

func ptrUint(v uint) *uint { return &v }
