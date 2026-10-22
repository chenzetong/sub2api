package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

type QuotaPoolConfig struct{ ent.Schema }

func (QuotaPoolConfig) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "quota_pool_config"}}
}

func (QuotaPoolConfig) Fields() []ent.Field {
	return []ent.Field{field.Int64("revision").Default(1), field.Bool("enabled").Default(false), field.Int("interval_minutes").Default(5), field.Time("next_run_at").Optional().Nillable()}
}
