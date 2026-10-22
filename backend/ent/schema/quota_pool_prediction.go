package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

type QuotaPoolPrediction struct{ ent.Schema }

func (QuotaPoolPrediction) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "quota_pool_predictions"}}
}

func (QuotaPoolPrediction) Fields() []ent.Field {
	return []ent.Field{field.Int64("account_id").Unique(), field.String("identity").MaxLen(64), field.Float("capacity").SchemaType(map[string]string{dialect.Postgres: "numeric(20,8)"}), field.Time("observed_at"), field.Time("window_start"), field.Time("window_end")}
}
