package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// These entities describe the persisted state machine. Store converts them to
// domain rows; generated Ent types never leave the persistence package.

type Session struct{ ent.Schema }

func (Session) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "sessions"}}
}
func (Session) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"), field.String("spec"), field.String("environment"),
		field.String("idempotency_key").Optional().Nillable(),
		field.String("creation_digest").Optional().Nillable(),
		field.String("environment_digest"), field.String("workspace"), field.String("status"),
		field.String("native_id").Optional().Nillable(), field.Float("created_at"),
		field.Float("completed_at").Optional().Nillable(),
		field.Float("cancelled_at").Optional().Nillable(),
		field.String("cancel_reason").Optional().Nillable(),
		field.String("parent_session_id").Optional().Nillable(),
		field.String("parent_task_id").Optional().Nillable(),
		field.String("parent_invocation_id").Optional().Nillable(),
		field.String("delegate_agent_id").Optional().Nillable(),
		field.String("delegation_request_digest").Optional().Nillable(),
	}
}
func (Session) Edges() []ent.Edge {
	return []ent.Edge{edge.To("tasks", Task.Type), edge.To("checkpoints", Checkpoint.Type), edge.To("events", Event.Type)}
}
func (Session) Indexes() []ent.Index {
	return []ent.Index{index.Fields("idempotency_key").Unique(), index.Fields("parent_task_id")}
}

type Task struct{ ent.Schema }

func (Task) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "tasks"}}
}
func (Task) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"), field.String("session_id"), field.Int("sequence"),
		field.String("idempotency_key"), field.String("spec"), field.String("spec_digest"),
		field.String("status"), field.String("candidate_id").Optional().Nillable(),
		field.String("error_code").Optional().Nillable(), field.Float("created_at"),
	}
}
func (Task) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("session", Session.Type).Ref("tasks").Field("session_id").Unique().Required(),
		edge.To("turns", Turn.Type), edge.To("candidates", Candidate.Type),
		edge.To("bases", TaskBase.Type), edge.To("checkpoints", Checkpoint.Type), edge.To("events", Event.Type),
	}
}
func (Task) Indexes() []ent.Index {
	return []ent.Index{index.Fields("session_id", "idempotency_key").Unique(), index.Fields("session_id", "sequence").Unique()}
}

type Turn struct{ ent.Schema }

func (Turn) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "turns"}}
}
func (Turn) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"), field.String("task_id"), field.Int("sequence"),
		field.String("input"), field.String("status"), field.String("invocation_id").Optional().Nillable(),
		field.Float("created_at"),
	}
}
func (Turn) Edges() []ent.Edge {
	return []ent.Edge{edge.From("task", Task.Type).Ref("turns").Field("task_id").Unique().Required(), edge.To("invocations", Invocation.Type)}
}
func (Turn) Indexes() []ent.Index { return []ent.Index{index.Fields("task_id", "sequence").Unique()} }

type Invocation struct{ ent.Schema }

func (Invocation) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "invocations"}}
}
func (Invocation) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"), field.String("turn_id"), field.String("runtime"), field.String("model"),
		field.String("sandbox_backend"), field.String("status"), field.Int("exit_code").Optional().Nillable(),
		field.String("native_id").Optional().Nillable(), field.String("log_path").Optional().Nillable(),
		field.Float("started_at"), field.Float("ended_at").Optional().Nillable(),
	}
}
func (Invocation) Edges() []ent.Edge {
	return []ent.Edge{edge.From("turn", Turn.Type).Ref("invocations").Field("turn_id").Unique().Required()}
}

type Candidate struct{ ent.Schema }

func (Candidate) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "candidates"}}
}
func (Candidate) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"), field.String("task_id"), field.String("vector"), field.String("digest"),
		field.String("checks"), field.String("deliverables").Default("[]"),
		field.String("status"), field.Float("created_at"),
	}
}
func (Candidate) Edges() []ent.Edge {
	return []ent.Edge{edge.From("task", Task.Type).Ref("candidates").Field("task_id").Unique().Required()}
}

type Checkpoint struct{ ent.Schema }

func (Checkpoint) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "checkpoints"}}
}
func (Checkpoint) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"), field.String("session_id"), field.String("task_id").Optional().Nillable(),
		field.String("archive_path"), field.String("metadata"), field.Float("created_at"),
	}
}
func (Checkpoint) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("session", Session.Type).Ref("checkpoints").Field("session_id").Unique().Required(),
		edge.From("task", Task.Type).Ref("checkpoints").Field("task_id").Unique(),
	}
}

type Event struct{ ent.Schema }

func (Event) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "events"}}
}
func (Event) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").StorageKey("sequence"), field.String("session_id"),
		field.String("task_id").Optional().Nillable(), field.String("type"), field.String("payload"),
		field.Float("created_at"),
	}
}
func (Event) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("session", Session.Type).Ref("events").Field("session_id").Unique().Required(),
		edge.From("task", Task.Type).Ref("events").Field("task_id").Unique(),
	}
}

// TaskBase stores a repository's immutable head at the beginning of a task.
// The old prototype table lacked an Ent-compatible ID, so it is migrated once
// into this table when an existing database is opened.
type TaskBase struct{ ent.Schema }

func (TaskBase) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "task_bases"}}
}
func (TaskBase) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"), field.String("task_id"), field.String("repo_id"), field.String("base_commit"),
	}
}
func (TaskBase) Edges() []ent.Edge {
	return []ent.Edge{edge.From("task", Task.Type).Ref("bases").Field("task_id").Unique().Required()}
}
func (TaskBase) Indexes() []ent.Index {
	return []ent.Index{index.Fields("task_id", "repo_id").Unique()}
}
