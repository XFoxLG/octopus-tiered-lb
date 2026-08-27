package pool

import (
	"errors"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

// OnPoolDeletedHooks 池删除时的清理钩子（由 relay 层注入）。
var OnPoolDeletedHooks []func(poolID int)

// OnPoolAccountDeletedHooks 账号删除时的清理钩子。
var OnPoolAccountDeletedHooks []func(poolID, accountID int)

// --- Pool CRUD ---

func ListPools() ([]model.AccountPool, error) {
	var pools []model.AccountPool
	err := db.GetDB().Order("id").Find(&pools).Error
	return pools, err
}

func GetPool(id int) (*model.AccountPool, error) {
	var pool model.AccountPool
	if err := db.GetDB().First(&pool, id).Error; err != nil {
		return nil, err
	}
	return &pool, nil
}

func CreatePool(pool *model.AccountPool) error {
	if pool.Name == "" {
		return errors.New("pool name is required")
	}
	if pool.Strategy == "" {
		pool.Strategy = "ewma"
	}
	switch pool.Strategy {
	case "ewma", "round_robin", "random", "least_loaded", "tiered_adaptive":
	default:
		return errors.New("unsupported pool strategy")
	}
	if pool.DefaultConcurrency <= 0 {
		pool.DefaultConcurrency = 1
	}
	if pool.CooldownBaseSec <= 0 {
		pool.CooldownBaseSec = 300
	}
	return db.GetDB().Create(pool).Error
}

func UpdatePool(id int, updates map[string]interface{}) error {
	result := db.GetDB().Model(&model.AccountPool{}).Where("id = ?", id).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func DeletePool(id int) error {
	tx := db.GetDB().Begin()
	// 删除池内所有账号。
	if err := tx.Where("pool_id = ?", id).Delete(&model.PoolAccount{}).Error; err != nil {
		tx.Rollback()
		return err
	}
	// 解除渠道关联。
	if err := tx.Model(&model.Channel{}).Where("pool_id = ?", id).Update("pool_id", 0).Error; err != nil {
		tx.Rollback()
		return err
	}
	// 删除池。
	if err := tx.Delete(&model.AccountPool{}, id).Error; err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit().Error; err != nil {
		return err
	}
	for _, hook := range OnPoolDeletedHooks {
		hook(id)
	}
	return nil
}

// --- Account CRUD ---

func ListAccounts(poolID int) ([]model.PoolAccount, error) {
	var accounts []model.PoolAccount
	err := db.GetDB().Where("pool_id = ?", poolID).Order("priority DESC, id").Find(&accounts).Error
	return accounts, err
}

func GetAccount(poolID, accountID int) (*model.PoolAccount, error) {
	var account model.PoolAccount
	if err := db.GetDB().Where("pool_id = ? AND id = ?", poolID, accountID).First(&account).Error; err != nil {
		return nil, err
	}
	return &account, nil
}

func CreateAccount(account *model.PoolAccount) error {
	if account.PoolID <= 0 {
		return errors.New("pool_id is required")
	}
	if account.Status == "" {
		account.Status = "active"
	}
	return db.GetDB().Create(account).Error
}

func UpdateAccount(poolID, accountID int, updates map[string]interface{}) error {
	result := db.GetDB().Model(&model.PoolAccount{}).
		Where("pool_id = ? AND id = ?", poolID, accountID).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func DeleteAccount(poolID, accountID int) error {
	result := db.GetDB().Where("pool_id = ? AND id = ?", poolID, accountID).Delete(&model.PoolAccount{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	for _, hook := range OnPoolAccountDeletedHooks {
		hook(poolID, accountID)
	}
	return nil
}

// ListSchedulableAccounts 返回指定池中当前可调度的账号列表。
func ListSchedulableAccounts(poolID int) ([]model.PoolAccount, error) {
	var accounts []model.PoolAccount
	err := db.GetDB().
		Where("pool_id = ? AND status = 'active' AND schedulable = ?", poolID, true).
		Order("priority DESC, id").
		Find(&accounts).Error
	if err != nil {
		return nil, err
	}
	// 在 Go 层过滤冷却（时间戳比较跨方言更可靠）。
	result := make([]model.PoolAccount, 0, len(accounts))
	for i := range accounts {
		if accounts[i].IsSchedulable() {
			result = append(result, accounts[i])
		}
	}
	return result, nil
}

// --- Account Test / Import ---

// AccountTestResult 账号连通性测试结果。
type AccountTestResult struct {
	Success bool   `json:"success"`
	Status  int    `json:"status"`
	Latency int64  `json:"latency_ms"`
	Error   string `json:"error,omitempty"`
}

// ImportAccounts 批量导入账号。已在调用方完成解析与凭据加密。
func ImportAccounts(accounts []model.PoolAccount) error {
	if len(accounts) == 0 {
		return nil
	}
	for i := range accounts {
		if accounts[i].PoolID <= 0 {
			return errors.New("pool_id is required")
		}
		if accounts[i].Status == "" {
			accounts[i].Status = "active"
		}
	}
	return db.GetDB().Create(&accounts).Error
}

// ListAllAccounts 返回所有池的所有账号（供后台刷新/额度同步任务遍历）。
func ListAllAccounts() ([]model.PoolAccount, error) {
	var accounts []model.PoolAccount
	err := db.GetDB().Order("id").Find(&accounts).Error
	return accounts, err
}
