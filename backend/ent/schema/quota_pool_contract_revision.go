package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"time"
)

type QuotaPoolContractRevision struct{ ent.Schema }

func (QuotaPoolContractRevision) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "quota_pool_contract_revisions"}}
}
func (QuotaPoolContractRevision) Fields() []ent.Field {
	return []ent.Field{field.Int64("revision").Unique(), field.JSON("allocation", map[string]any{}), field.Time("created_at").Default(time.Now)}
}
