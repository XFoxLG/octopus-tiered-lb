package stats

import (
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

type dialectNameStub string

func (d dialectNameStub) Name() string                                          { return string(d) }
func (d dialectNameStub) Initialize(*gorm.DB) error                             { return nil }
func (d dialectNameStub) Migrator(*gorm.DB) gorm.Migrator                       { return nil }
func (d dialectNameStub) DataTypeOf(*schema.Field) string                       { return "" }
func (d dialectNameStub) DefaultValueOf(*schema.Field) clause.Expression        { return clause.Expr{} }
func (d dialectNameStub) BindVarTo(clause.Writer, *gorm.Statement, interface{}) {}
func (d dialectNameStub) QuoteTo(clause.Writer, string)                         {}
func (d dialectNameStub) Explain(sql string, _ ...interface{}) string           { return sql }

// These expressions are emitted inside ON CONFLICT clauses. A dialect-specific
// regression therefore has to be checked at SQL-generation time: SQLite has no
// GREATEST, MySQL does not treat double quotes as identifiers by default, and
// PostgreSQL requires table-qualified columns to avoid ambiguous references.
func TestDailyDimensionExpressionsUseDialectQualifiedColumns(t *testing.T) {
	newDialect := func(name string) *gorm.DB {
		return &gorm.DB{Config: &gorm.Config{Dialector: dialectNameStub(name)}}
	}

	mysqlDatabase := newDialect("mysql")
	if got := dailyDimensionCurrentExpr(mysqlDatabase, "stats_daily_channels", "input_token"); got != "`stats_daily_channels`.`input_token`" {
		t.Fatalf("mysql current expression = %q", got)
	}
	if got := dailyDimensionMaxExpr(mysqlDatabase, "stats_daily_channels", "input_token"); got != "GREATEST(`stats_daily_channels`.`input_token`, VALUES(`input_token`))" {
		t.Fatalf("mysql max expression = %q", got)
	}

	postgresDatabase := newDialect("postgres")
	if got := dailyDimensionCurrentExpr(postgresDatabase, "stats_daily_channels", "input_token"); got != `"stats_daily_channels"."input_token"` {
		t.Fatalf("postgres current expression = %q", got)
	}
	if got := dailyDimensionMaxExpr(postgresDatabase, "stats_daily_channels", "input_token"); got != `GREATEST("stats_daily_channels"."input_token", excluded.input_token)` {
		t.Fatalf("postgres max expression = %q", got)
	}

	sqliteDatabase := newDialect("sqlite")
	if got := dailyDimensionCurrentExpr(sqliteDatabase, "stats_daily_channels", "input_token"); got != `"stats_daily_channels"."input_token"` {
		t.Fatalf("sqlite current expression = %q", got)
	}
	if got := dailyDimensionMaxExpr(sqliteDatabase, "stats_daily_channels", "input_token"); got != `MAX("stats_daily_channels"."input_token", excluded.input_token)` {
		t.Fatalf("sqlite max expression = %q", got)
	}
}
