package dto

// DraftScheduleResponse describes the current unpublished draft timetable.
type DraftScheduleResponse struct {
	Exists    bool               `json:"exists"`
	Total     int                `json:"total"`
	Weeks     []uint             `json:"weeks"`
	Schedules []ScheduleResponse `json:"schedules"`
	Conflicts []ConflictResponse `json:"conflicts"`
}

// PublishScheduleRequest promotes draft entries to the live timetable. An
// empty weeks list publishes all draft weeks.
type PublishScheduleRequest struct {
	Weeks []uint `json:"weeks" binding:"omitempty,dive,gte=1"`
	Note  string `json:"note" binding:"omitempty,max=256"`
}

// PublishScheduleResponse is returned after a successful draft publish.
type PublishScheduleResponse struct {
	VersionID      uint               `json:"version_id"`
	Version        int                `json:"version"`
	PublishedWeeks []uint             `json:"published_weeks"`
	PublishedCount int                `json:"published_count"`
	LiveCount      int                `json:"live_count"`
	PublishedAt    string             `json:"published_at"`
	Schedules      []ScheduleResponse `json:"schedules"`
}

// ScheduleVersionResponse is one published timetable version summary.
type ScheduleVersionResponse struct {
	ID          uint   `json:"id"`
	Version     int    `json:"version"`
	Status      string `json:"status"`
	EntryCount  int    `json:"entry_count"`
	PublishedAt string `json:"published_at"`
	Note        string `json:"note"`
	CreatedAt   string `json:"created_at"`
}
