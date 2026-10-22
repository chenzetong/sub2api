package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

type QuotaPoolManagedUser struct{ ent.Schema }

func (QuotaPoolManagedUser) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "quota_pool_managed_users"}}
}

func (QuotaPoolManagedUser) Fields() []ent.Field {
	return []ent.Field{field.Int64("user_id").Unique(), field.Bool("managed").Default(true)}
}
