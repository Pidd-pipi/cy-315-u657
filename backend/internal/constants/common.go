package constants

// Pagination defaults and limits.
const (
	DefaultPage     = 1
	DefaultPageSize = 20
	MaxPageSize     = 200
)

// Adjustment action names.
const (
	ActionSwap    = "swap"
	ActionMove    = "move"
	ActionPublish = "publish"
)

// Schedule lifecycle statuses.
const (
	// ScheduleStatusDraft holds pending changes until they are published.
	ScheduleStatusDraft = "draft"
	// ScheduleStatusPublished marks entries of an official, immutable version.
	ScheduleStatusPublished = "published"
)
