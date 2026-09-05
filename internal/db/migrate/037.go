package migrate

import (
	"fmt"

	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 37,
		Up:      addHistogramColumnsToStatsTablesIfMissing,
	})
}

// 037: 为所有统计表增加延迟直方图列（issue #159 导入数据后缺失 histogram 列）。
// 这些列用于记录延迟分布：< 100ms, 100-500ms, 500ms-1s, 1-5s, > 5s。
// GORM AutoMigrate 通常也会加列，这里幂等兜底，确保跨方言与导入数据场景下列存在。
func addHistogramColumnsToStatsTablesIfMissing(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}

	// 定义需要添加 histogram 列的所有统计表及其模型
	tables := []struct {
		model   interface{}
		columns []string
	}{
		{&model.StatsTotal{}, []string{"HistogramLt100", "Histogram100to500", "Histogram500to1k", "Histogram1kto5k", "HistogramGt5k"}},
		{&model.StatsHourly{}, []string{"HistogramLt100", "Histogram100to500", "Histogram500to1k", "Histogram1kto5k", "HistogramGt5k"}},
		{&model.StatsDaily{}, []string{"HistogramLt100", "Histogram100to500", "Histogram500to1k", "Histogram1kto5k", "HistogramGt5k"}},
		{&model.StatsDailyChannel{}, []string{"HistogramLt100", "Histogram100to500", "Histogram500to1k", "Histogram1kto5k", "HistogramGt5k"}},
		{&model.StatsDailyModel{}, []string{"HistogramLt100", "Histogram100to500", "Histogram500to1k", "Histogram1kto5k", "HistogramGt5k"}},
		{&model.StatsDailyAPIKey{}, []string{"HistogramLt100", "Histogram100to500", "Histogram500to1k", "Histogram1kto5k", "HistogramGt5k"}},
		{&model.StatsDailyChannelModel{}, []string{"HistogramLt100", "Histogram100to500", "Histogram500to1k", "Histogram1kto5k", "HistogramGt5k"}},
		{&model.StatsChannel{}, []string{"HistogramLt100", "Histogram100to500", "Histogram500to1k", "Histogram1kto5k", "HistogramGt5k"}},
		{&model.StatsAPIKey{}, []string{"HistogramLt100", "Histogram100to500", "Histogram500to1k", "Histogram1kto5k", "HistogramGt5k"}},
	}

	for _, tbl := range tables {
		if !db.Migrator().HasTable(tbl.model) {
			continue
		}
		for _, col := range tbl.columns {
			if db.Migrator().HasColumn(tbl.model, col) {
				continue
			}
			if err := db.Migrator().AddColumn(tbl.model, col); err != nil {
				return fmt.Errorf("add %T.%s: %w", tbl.model, col, err)
			}
		}
	}
	return nil
}
