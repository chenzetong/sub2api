package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

type QuotaPool struct{ ent.Schema }

func (QuotaPool) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "quota_pools"}}
}

func (QuotaPool) Fields() []ent.Field {
	return []ent.Field{field.String("name").MaxLen(160), field.Int64("revision").Default(1)}
}
