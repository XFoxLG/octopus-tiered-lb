package model

type StatsMetrics struct {
	InputToken     int64   `json:"input_token" gorm:"bigint"`
	OutputToken    int64   `json:"output_token" gorm:"bigint"`
	InputCost      float64 `json:"input_cost" gorm:"type:real"`
	OutputCost     float64 `json:"output_cost" gorm:"type:real"`
	WaitTime       int64   `json:"wait_time" gorm:"bigint"`
	RequestSuccess int64   `json:"request_success" gorm:"bigint"`
	RequestFailed  int64   `json:"request_failed" gorm:"bigint"`

	// 延迟分布统计（毫秒）
	LatencyP50 int64 `json:"latency_p50" gorm:"bigint"`
	LatencyP95 int64 `json:"latency_p95" gorm:"bigint"`
	LatencyP99 int64 `json:"latency_p99" gorm:"bigint"`

	// 首 Token 时间统计（毫秒）
	FtutAvg int64 `json:"ftut_avg" gorm:"bigint"`
	FtutP50 int64 `json:"ftut_p50" gorm:"bigint"`
	FtutP95 int64 `json:"ftut_p95" gorm:"bigint"`
	FtutP99 int64 `json:"ftut_p99" gorm:"bigint"`

	// 延迟直方图（请求计数）
	HistogramLt100    int64 `json:"histogram_lt_100" gorm:"bigint"`
	Histogram100to500 int64 `json:"histogram_100_500" gorm:"bigint"`
	Histogram500to1k  int64 `json:"histogram_500_1k" gorm:"bigint"`
	Histogram1kto5k   int64 `json:"histogram_1k_5k" gorm:"bigint"`
	HistogramGt5k     int64 `json:"histogram_gt_5k" gorm:"bigint"`
}

type StatsTotal struct {
	ID int `gorm:"primaryKey"`
	StatsMetrics
}

type StatsHourly struct {
	Hour int    `json:"hour" gorm:"primaryKey"`
	Date string `json:"date" gorm:"primaryKey;not null"` // 记录最后更新日期，格式：20060102
	StatsMetrics
}

type StatsDaily struct {
	Date string `json:"date" gorm:"primaryKey"`
	StatsMetrics
}

type StatsDailyChannel struct {
	Date        string `json:"date" gorm:"primaryKey;type:varchar(8);index:idx_stats_daily_channel_date"`
	ChannelID   int    `json:"channel_id" gorm:"primaryKey;autoIncrement:false;index:idx_stats_daily_channel_lookup"`
	ChannelName string `json:"channel_name" gorm:"type:varchar(255)"`
	StatsMetrics
}

type StatsDailyModel struct {
	Date      string `json:"date" gorm:"primaryKey;type:varchar(8);index:idx_stats_daily_model_date"`
	ModelName string `json:"model_name" gorm:"primaryKey;type:varchar(255);index:idx_stats_daily_model_lookup"`
	StatsMetrics
}

type StatsDailyAPIKey struct {
	Date     string `json:"date" gorm:"primaryKey;type:varchar(8);index:idx_stats_daily_apikey_date"`
	APIKeyID int    `json:"api_key_id" gorm:"primaryKey;autoIncrement:false;index:idx_stats_daily_apikey_lookup"`
	Name     string `json:"name" gorm:"type:varchar(255)"`
	StatsMetrics
}

type StatsDailyChannelModel struct {
	Date        string `json:"date" gorm:"primaryKey;type:varchar(8);index:idx_stats_daily_channel_model_date"`
	ChannelID   int    `json:"channel_id" gorm:"primaryKey;autoIncrement:false;index:idx_stats_daily_channel_model_lookup"`
	ChannelName string `json:"channel_name" gorm:"type:varchar(255)"`
	ModelName   string `json:"model_name" gorm:"primaryKey;type:varchar(255);index:idx_stats_daily_channel_model_lookup"`
	StatsMetrics
}

type StatsModel struct {
	ID        int64  `json:"id" gorm:"primaryKey"`
	Name      string `json:"name" gorm:"not null"`
	ChannelID int    `json:"channel_id" gorm:"not null"`
	StatsMetrics
}

type StatsChannel struct {
	ChannelID int `json:"channel_id" gorm:"primaryKey"`
	StatsMetrics
}

type StatsAPIKey struct {
	APIKeyID int `json:"api_key_id" gorm:"primaryKey"`
	StatsMetrics
}

// Add aggregates another StatsMetrics into the current one.
func (s *StatsMetrics) Add(delta StatsMetrics) {
	s.InputToken += delta.InputToken
	s.OutputToken += delta.OutputToken
	s.InputCost += delta.InputCost
	s.OutputCost += delta.OutputCost
	s.WaitTime += delta.WaitTime
	s.RequestSuccess += delta.RequestSuccess
	s.RequestFailed += delta.RequestFailed

	// 延迟百分位数取最大值（近似）
	if delta.LatencyP50 > s.LatencyP50 {
		s.LatencyP50 = delta.LatencyP50
	}
	if delta.LatencyP95 > s.LatencyP95 {
		s.LatencyP95 = delta.LatencyP95
	}
	if delta.LatencyP99 > s.LatencyP99 {
		s.LatencyP99 = delta.LatencyP99
	}

	// FTUT 百分位数取最大值（近似）
	if delta.FtutAvg > s.FtutAvg {
		s.FtutAvg = delta.FtutAvg
	}
	if delta.FtutP50 > s.FtutP50 {
		s.FtutP50 = delta.FtutP50
	}
	if delta.FtutP95 > s.FtutP95 {
		s.FtutP95 = delta.FtutP95
	}
	if delta.FtutP99 > s.FtutP99 {
		s.FtutP99 = delta.FtutP99
	}

	// 直方图累加
	s.HistogramLt100 += delta.HistogramLt100
	s.Histogram100to500 += delta.Histogram100to500
	s.Histogram500to1k += delta.Histogram500to1k
	s.Histogram1kto5k += delta.Histogram1kto5k
	s.HistogramGt5k += delta.HistogramGt5k
}

// IsZero reports whether the metrics have no accumulated data at all.
// Used by the idle-purge path (PurgeIdleModelStats) to distinguish truly
// garbage entries (random/spam model names that never produced a real
// request) from legitimate entries that merely went idle. Only zero-stat
// entries are safe to drop from the in-memory cache without making the
// model-market page lose data — the DB still has the record, but the
// model-market reads from the in-memory cache and RefreshCache only runs
// at startup, so dropping a non-zero entry would make that model's stats
// "disappear" from the UI until the next restart.
func (s StatsMetrics) IsZero() bool {
	return s.InputToken == 0 &&
		s.OutputToken == 0 &&
		s.InputCost == 0 &&
		s.OutputCost == 0 &&
		s.WaitTime == 0 &&
		s.RequestSuccess == 0 &&
		s.RequestFailed == 0 &&
		s.LatencyP50 == 0 &&
		s.LatencyP95 == 0 &&
		s.LatencyP99 == 0 &&
		s.FtutAvg == 0 &&
		s.FtutP50 == 0 &&
		s.FtutP95 == 0 &&
		s.FtutP99 == 0 &&
		s.HistogramLt100 == 0 &&
		s.Histogram100to500 == 0 &&
		s.Histogram500to1k == 0 &&
		s.Histogram1kto5k == 0 &&
		s.HistogramGt5k == 0
}
