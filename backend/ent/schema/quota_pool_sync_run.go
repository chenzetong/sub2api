package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"time"
)

type QuotaPoolSyncRun struct{ ent.Schema }

func (QuotaPoolSyncRun) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "quota_pool_sync_runs"}}
}

func (QuotaPoolSyncRun) Fields() []ent.Field {
	return []ent.Field{field.String("operation_key").MaxLen(160).Unique(), field.String("source").MaxLen(20), field.String("status").MaxLen(32), field.JSON("result", map[string]any{}), field.Time("created_at").Default(time.Now)}
}
