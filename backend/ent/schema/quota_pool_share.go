package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type QuotaPoolShare struct{ ent.Schema }

func (QuotaPoolShare) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "quota_pool_shares"}}
}

func (QuotaPoolShare) Fields() []ent.Field {
	return []ent.Field{field.Int64("pool_id"), field.Int64("user_id"), field.Float("share_percent").SchemaType(map[string]string{dialect.Postgres: "numeric(6,3)"})}
}

func (QuotaPoolShare) Indexes() []ent.Index {
	return []ent.Index{index.Fields("pool_id", "user_id").Unique()}
}
