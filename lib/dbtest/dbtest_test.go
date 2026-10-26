package dbtest

import (
	"strings"
	"testing"
)

func TestNewScopesToSchema(t *testing.T) {
	db := New(t)
	var current string
	if err := db.Raw("SELECT current_schema()").Scan(&current).Error; err != nil {
		t.Fatalf("current_schema: %v", err)
	}
	if !strings.HasPrefix(current, "t_testnewscopestoschema_") {
		t.Errorf("current_schema = %q, want the per-test schema", current)
	}
}

func TestSchemaName(t *testing.T) {
	got := SchemaName("TestFoo/sub-case " + strings.Repeat("x", 60))
	if !strings.HasPrefix(got, "t_testfoo_sub_case_xxx") {
		t.Errorf("SchemaName = %q", got)
	}
	if strings.ContainsAny(got, "/- ") {
		t.Errorf("SchemaName = %q contains characters invalid in an identifier", got)
	}
}
