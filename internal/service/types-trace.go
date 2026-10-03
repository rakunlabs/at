package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"
)

// ─── Trace explorer (Langfuse-style Session → Trace → Observation) ───

// TraceQuery filters the trace list. Attribute filters (model, source, ...)
// match a trace when ANY of its observations matches, so the aggregates of a
// matching trace always describe the whole trace rather than the matching
// subset. Numeric bounds apply to the trace aggregates.
type TraceQuery struct {
	From time.Time
	To   time.Time

	// Search matches the trace ID, trace name or trace input preview.
	Search string

	TraceIDs     []string
	Names        []string
	UserIDs      []string
	EndUsers     []string
	SessionIDs   []string
	TokenIDs     []string
	TaskIDs      []string
	AgentIDs     []string
	Models       []string
	Sources      []string
	Tags         []string
	Environments []string
	Releases     []string
	// Status is "" | "ok" | "error". Error means at least one failed
	// observation.
	Status string

	MinLatencyMs *int64
	MaxLatencyMs *int64
	MinCostCents *float64
	MaxCostCents *float64
	MinTokens    *int64
	MaxTokens    *int64

	// ScoreName restricts to traces carrying that score; MinScore/MaxScore
	// bound its value.
	ScoreName string
	MinScore  *float64
	MaxScore  *float64

	// Bookmarked restricts to the caller's bookmarks.
	Bookmarked bool

	// Sort is one of TraceSortFields; Desc reverses it. Default started_at desc.
	Sort   string
	Desc   bool
	Offset uint64
	Limit  uint64
}

// TraceSortFields are the sortable trace-list columns.
var TraceSortFields = []string{"started_at", "duration", "latency", "cost", "tokens", "errors", "observations"}

// TraceScoreSummary aggregates one score name on one trace.
type TraceScoreSummary struct {
	Name        string   `json:"name"`
	DataType    string   `json:"data_type"`
	Average     *float64 `json:"average,omitempty"`
	StringValue string   `json:"string_value,omitempty"`
	Count       int64    `json:"count"`
}

// TraceSummary is one row of the trace list.
type TraceSummary struct {
	LLMCallTrace
	TokenID     string              `json:"token_id,omitempty"`
	UserID      string              `json:"user_id,omitempty"`
	EndUser     string              `json:"end_user,omitempty"`
	Environment string              `json:"environment,omitempty"`
	Release     string              `json:"release,omitempty"`
	Tags        []string            `json:"tags"`
	Models      []string            `json:"models"`
	Input       string              `json:"input,omitempty"`
	Output      string              `json:"output,omitempty"`
	DurationMs  int64               `json:"duration_ms"`
	TotalTokens int64               `json:"total_tokens"`
	Scores      []TraceScoreSummary `json:"scores"`
	Bookmarked  bool                `json:"bookmarked"`
}

// TraceDetail is a whole trace: its summary, every observation (with clipped
// bodies; full payloads come from the observation endpoint) and its scores.
type TraceDetail struct {
	Trace        TraceSummary `json:"trace"`
	Observations []LLMCall    `json:"observations"`
	// Truncated reports that the trace had more observations than
	// TraceObservationLimit and only the earliest are returned.
	Truncated bool         `json:"truncated"`
	Scores    []TraceScore `json:"scores"`
}

// TraceObservationLimit bounds one trace detail read.
const TraceObservationLimit = 5000

// TraceSessionQuery filters the session list.
type TraceSessionQuery struct {
	From    time.Time
	To      time.Time
	Search  string
	UserIDs []string
	Sources []string
	Offset  uint64
	Limit   uint64
}

// TraceSession groups the traces sharing one session ID within one API token
// namespace (two clients using "session-1" are never conflated).
type TraceSession struct {
	SessionID        string   `json:"session_id"`
	TokenID          string   `json:"token_id,omitempty"`
	UserID           string   `json:"user_id,omitempty"`
	EndUser          string   `json:"end_user,omitempty"`
	Sources          []string `json:"sources"`
	Name             string   `json:"name,omitempty"`
	TraceCount       int64    `json:"trace_count"`
	ObservationCount int64    `json:"observation_count"`
	GenerationCount  int64    `json:"generation_count"`
	InputTokens      int64    `json:"input_tokens"`
	OutputTokens     int64    `json:"output_tokens"`
	CostCents        float64  `json:"cost_cents"`
	ErrorCount       int64    `json:"error_count"`
	StartedAt        string   `json:"started_at"`
	EndedAt          string   `json:"ended_at"`
}

// TraceSessionDetail is a session with its traces in chronological order.
type TraceSessionDetail struct {
	Session TraceSession   `json:"session"`
	Traces  []TraceSummary `json:"traces"`
}

// TraceFacets lists the values present in a window, for filter pickers.
type TraceFacets struct {
	Names        []string `json:"names"`
	Models       []string `json:"models"`
	Sources      []string `json:"sources"`
	Environments []string `json:"environments"`
	Releases     []string `json:"releases"`
	Tags         []string `json:"tags"`
	UserIDs      []string `json:"user_ids"`
	EndUsers     []string `json:"end_users"`
	ScoreNames   []string `json:"score_names"`
}

// Score data types and sources.
const (
	ScoreNumeric     = "numeric"
	ScoreBoolean     = "boolean"
	ScoreCategorical = "categorical"

	ScoreSourceAnnotation = "annotation"
	ScoreSourceAPI        = "api"
)

// TraceScore is a quality metric attached to a trace or one observation.
type TraceScore struct {
	ID            string   `json:"id"`
	TraceID       string   `json:"trace_id"`
	ObservationID string   `json:"observation_id,omitempty"`
	Name          string   `json:"name"`
	DataType      string   `json:"data_type"`
	Value         *float64 `json:"value,omitempty"`
	StringValue   string   `json:"string_value,omitempty"`
	Source        string   `json:"source"`
	Comment       string   `json:"comment,omitempty"`
	AuthorUserID  string   `json:"author_user_id,omitempty"`
	TokenID       string   `json:"token_id,omitempty"`
	CreatedAt     string   `json:"created_at"`
}

// ErrTraceNotFound reports a trace or observation outside the caller's reach.
var ErrTraceNotFound = errors.New("trace not found")

// ValidateTraceScore normalizes and validates a score before it is stored.
// Boolean scores store 0/1 in Value; categorical scores store StringValue.
func ValidateTraceScore(s *TraceScore) error {
	s.TraceID = strings.TrimSpace(s.TraceID)
	s.ObservationID = strings.TrimSpace(s.ObservationID)
	s.Name = strings.TrimSpace(s.Name)
	s.StringValue = strings.TrimSpace(s.StringValue)
	s.Comment = strings.TrimSpace(s.Comment)
	if s.TraceID == "" || len(s.TraceID) > 256 {
		return fmt.Errorf("trace_id is required")
	}
	if len(s.ObservationID) > 256 {
		return fmt.Errorf("observation_id is too long")
	}
	if s.Name == "" || len(s.Name) > 64 || strings.ContainsFunc(s.Name, unicode.IsControl) {
		return fmt.Errorf("name is required (at most 64 characters)")
	}
	if len(s.Comment) > 4000 {
		return fmt.Errorf("comment is too long (at most 4000 bytes)")
	}
	if s.DataType == "" {
		switch {
		case s.Value != nil:
			s.DataType = ScoreNumeric
		case s.StringValue != "":
			s.DataType = ScoreCategorical
		}
	}
	switch s.DataType {
	case ScoreNumeric:
		if s.Value == nil || math.IsNaN(*s.Value) || math.IsInf(*s.Value, 0) {
			return fmt.Errorf("numeric score needs a finite value")
		}
		s.StringValue = ""
	case ScoreBoolean:
		if s.Value == nil || (*s.Value != 0 && *s.Value != 1) {
			return fmt.Errorf("boolean score needs value 0 or 1")
		}
		s.StringValue = ""
	case ScoreCategorical:
		if s.StringValue == "" || len(s.StringValue) > 128 {
			return fmt.Errorf("categorical score needs string_value (at most 128 characters)")
		}
		s.Value = nil
	default:
		return fmt.Errorf("data_type must be numeric, boolean or categorical")
	}
	return nil
}

// TraceStorer is the read model of the trace explorer plus scores and
// bookmarks. Every method is bounded to the caller's workspace.
type TraceStorer interface {
	ListTraces(ctx context.Context, q TraceQuery) (*ListResult[TraceSummary], error)
	GetTrace(ctx context.Context, traceID string) (*TraceDetail, error)
	ListTraceSessions(ctx context.Context, q TraceSessionQuery) (*ListResult[TraceSession], error)
	GetTraceSession(ctx context.Context, sessionID, tokenID string) (*TraceSessionDetail, error)
	GetTraceFacets(ctx context.Context, from time.Time) (*TraceFacets, error)

	// CreateTraceScore stores an annotation from the authenticated account.
	CreateTraceScore(ctx context.Context, score TraceScore) (*TraceScore, error)
	// CreateTraceScoreForToken stores an API score; the trace must contain an
	// observation recorded with that token in the token's workspace.
	CreateTraceScoreForToken(ctx context.Context, workspaceID, tokenID string, score TraceScore) (*TraceScore, error)
	// DeleteTraceScore removes a score. Annotations may only be removed by
	// their author (or a platform administrator).
	DeleteTraceScore(ctx context.Context, id string) error
	SetTraceBookmark(ctx context.Context, traceID string, bookmarked bool) error
}
