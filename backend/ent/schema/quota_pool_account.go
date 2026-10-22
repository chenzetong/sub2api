package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

type QuotaPoolAccount struct{ ent.Schema }

func (QuotaPoolAccount) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "quota_pool_accounts"}}
}

func (QuotaPoolAccount) Fields() []ent.Field {
	return []ent.Field{field.Int64("pool_id"), field.Int64("account_id").Unique()}
}
