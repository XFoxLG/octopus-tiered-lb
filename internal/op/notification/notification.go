package notification

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

var timeNow = func() int64 { return time.Now().UnixMilli() }

type ListFilter struct {
	Type      string
	Severity  string
	Source    string
	Read      *bool
	Archived  *bool
	Search    string
	StartTime *int64
	EndTime   *int64
}

func Create(ctx context.Context, n *model.Notification) error {
	if n == nil {
		return fmt.Errorf("notification is nil")
	}
	n.Type = normalizeType(n.Type)
	n.Severity = normalizeSeverity(n.Severity)
	if strings.TrimSpace(n.Title) == "" {
		return fmt.Errorf("notification title cannot be empty")
	}
	if err := db.GetDB().WithContext(ctx).Create(n).Error; err != nil {
		return err
	}
	Publish(*n)
	return nil
}

func List(ctx context.Context, filter ListFilter, page, pageSize int) ([]model.Notification, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	q := applyListFilter(db.GetDB().WithContext(ctx).Model(&model.Notification{}), filter)
	var items []model.Notification
	if err := q.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

func Get(ctx context.Context, id int64) (*model.Notification, error) {
	var n model.Notification
	if err := db.GetDB().WithContext(ctx).First(&n, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &n, nil
}

func UnreadCount(ctx context.Context, archived bool) (int64, error) {
	q := db.GetDB().WithContext(ctx).Model(&model.Notification{}).Where("read_at IS NULL")
	if !archived {
		q = q.Where("archived_at IS NULL")
	}
	var count int64
	if err := q.Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func MarkRead(ctx context.Context, ids []int64) error {
	return updateReadAt(ctx, ids, ptrInt64(timeNow()))
}

func MarkUnread(ctx context.Context, ids []int64) error {
	return updateReadAt(ctx, ids, nil)
}

func MarkAllRead(ctx context.Context) error {
	now := timeNow()
	return db.GetDB().WithContext(ctx).Model(&model.Notification{}).Where("read_at IS NULL").Update("read_at", now).Error
}

func Archive(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	now := timeNow()
	return db.GetDB().WithContext(ctx).Model(&model.Notification{}).Where("id IN ?", ids).Update("archived_at", now).Error
}

func Unarchive(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	return db.GetDB().WithContext(ctx).Model(&model.Notification{}).Where("id IN ?", ids).Update("archived_at", nil).Error
}

func Delete(ctx context.Context, id int64) error {
	return db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Delete(&model.Notification{}, id)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("notification not found")
		}
		return nil
	})
}

func DeleteArchived(ctx context.Context) error {
	return db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ids []int64
		if err := tx.Model(&model.Notification{}).Where("archived_at IS NOT NULL").Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		return tx.Where("id IN ?", ids).Delete(&model.Notification{}).Error
	})
}

func applyListFilter(q *gorm.DB, filter ListFilter) *gorm.DB {
	if filter.Type != "" {
		q = q.Where("type = ?", filter.Type)
	}
	if filter.Severity != "" {
		q = q.Where("severity = ?", filter.Severity)
	}
	if filter.Source != "" {
		q = q.Where("source = ?", filter.Source)
	}
	if filter.Read != nil {
		if *filter.Read {
			q = q.Where("read_at IS NOT NULL")
		} else {
			q = q.Where("read_at IS NULL")
		}
	}
	if filter.Archived != nil {
		if *filter.Archived {
			q = q.Where("archived_at IS NOT NULL")
		} else {
			q = q.Where("archived_at IS NULL")
		}
	} else {
		q = q.Where("archived_at IS NULL")
	}
	if filter.Search != "" {
		like := "%" + filter.Search + "%"
		q = q.Where("title LIKE ? OR content LIKE ?", like, like)
	}
	if filter.StartTime != nil {
		q = q.Where("created_at >= ?", *filter.StartTime)
	}
	if filter.EndTime != nil {
		q = q.Where("created_at <= ?", *filter.EndTime)
	}
	return q
}

func updateReadAt(ctx context.Context, ids []int64, readAt *int64) error {
	if len(ids) == 0 {
		return nil
	}
	return db.GetDB().WithContext(ctx).Model(&model.Notification{}).Where("id IN ?", ids).Update("read_at", readAt).Error
}

func ptrInt64(v int64) *int64 { return &v }

func normalizeType(t model.NotificationType) model.NotificationType {
	if strings.TrimSpace(string(t)) == "" {
		return model.NotificationTypeSystem
	}
	return t
}

func normalizeSeverity(s model.NotificationSeverity) model.NotificationSeverity {
	switch s {
	case model.NotificationSeveritySuccess, model.NotificationSeverityWarning, model.NotificationSeverityError, model.NotificationSeverityCritical:
		return s
	default:
		return model.NotificationSeverityInfo
	}
}
