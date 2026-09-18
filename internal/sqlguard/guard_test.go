package sqlguard

import "testing"

func TestAllowsPlainReads(t *testing.T) {
	ok := []string{
		"select * from users",
		"SELECT id, name FROM users WHERE org_id = 1",
		"with recent as (select * from users) select * from recent",
		"select * from users -- trailing comment",
		"select 'insert this string' as note", // keyword inside a string literal
		"select * from users; ",               // trailing semicolon + whitespace only
	}
	for _, sql := range ok {
		if err := AssertReadOnlySelect(sql); err != nil {
			t.Errorf("expected %q to be allowed, got error: %v", sql, err)
		}
	}
}

func TestRejectsMutationsAndMultipleStatements(t *testing.T) {
	bad := []string{
		"delete from users",
		"update users set name = 'x'",
		"drop table users",
		"insert into users (id, name) values (3, 'Eve')",
		"select * from users; drop table users",
		"select * from users; select * from orgs",
		"",
		"   ",
		"create table x (id int)",
		"truncate table users",
		"select * into backup_users from users",
	}
	for _, sql := range bad {
		if err := AssertReadOnlySelect(sql); err == nil {
			t.Errorf("expected %q to be rejected, but it was allowed", sql)
		}
	}
}
