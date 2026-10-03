package db

import (
	"github.com/nickalie/nclaw/internal/model"
	"gorm.io/gorm"
)

// LogRun records a task execution result.
func LogRun(database *gorm.DB, entry *model.TaskRunLog) error {
	return database.Create(entry).Error
}

// PruneRunLogs deletes all but the newest keep run logs of a task.
func PruneRunLogs(database *gorm.DB, taskID string, keep int) error {
	newest := database.Model(&model.TaskRunLog{}).Select("id").Where("task_id = ?", taskID).Order("id DESC").Limit(keep)
	return database.Where("task_id = ? AND id NOT IN (?)", taskID, newest).Delete(&model.TaskRunLog{}).Error
}
