package postgres

import "github.com/doug-martin/goqu/v9"

func routingProfileScope(owner string) string {
	if owner != "" {
		return "personal"
	}
	return "workspace"
}

func routingProfileOwnerPredicate(owner ...string) goqu.Expression {
	if len(owner) == 0 || owner[0] == "" {
		return goqu.C("owner_user_id").Eq("")
	}
	return goqu.C("owner_user_id").In("", owner[0])
}
