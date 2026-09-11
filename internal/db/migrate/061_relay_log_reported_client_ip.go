package migrate

import (
	"fmt"

	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 61,
		Up:      addRelayLogReportedClientIP,
	})
}

// 061: 为 relay_logs 增加展示轨来源 IP 两列。
// reported_client_ip 记录按转发头（CF-Connecting-IP 优先、XFF 右起首个公网
// 地址回退）解析出的客户端来源，仅用于日志展示；client_ip 保持 Gin 安全
// 解析（可信代理链）语义不变，继续驱动限流与 IP 白名单。
// reported_client_ip_source 仅标注取值来源头，不保证来源可信；none 表示没有
// 可用地址（可能缺头、格式错误或只有内网地址）。既有行不回填，前端回退
// 显示原 client_ip。
func addRelayLogReportedClientIP(database *gorm.DB) error {
	if database == nil {
		return fmt.Errorf("db is nil")
	}
	if !database.Migrator().HasTable(&model.RelayLog{}) {
		return nil
	}
	for _, col := range []string{"ReportedClientIP", "ReportedClientIPSource"} {
		if database.Migrator().HasColumn(&model.RelayLog{}, col) {
			continue
		}
		if err := database.Migrator().AddColumn(&model.RelayLog{}, col); err != nil {
			return fmt.Errorf("add relay_logs.%s: %w", col, err)
		}
	}
	return nil
}
