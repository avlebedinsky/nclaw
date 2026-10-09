package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/nickalie/nclaw/internal/blocks"
	"github.com/nickalie/nclaw/internal/chatqueue"
	"github.com/nickalie/nclaw/internal/cli"
	"github.com/nickalie/nclaw/internal/invoker"
	"github.com/nickalie/nclaw/internal/model"
)

// mockProvider implements cli.Provider for testing.
type mockProvider struct{}

func (m *mockProvider) NewClient() cli.Client { return nil }
func (m *mockProvider) PreInvoke() error      { return nil }
func (m *mockProvider) Version() (string, error) {
	return "mock-1.0.0", nil
}
func (m *mockProvider) Name() string { return "mock" }

type inlineRunner struct{}

func (inlineRunner) Do(_ chatqueue.Key, job chatqueue.Job) error {
	job.Fn(context.Background())
	return nil
}

func setupTestScheduler(t *testing.T) *Scheduler {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(&model.ScheduledTask{}, &model.TaskRunLog{}))

	sched, err := New(database, invoker.New(&mockProvider{}, invoker.Options{DataDir: t.TempDir()}), inlineRunner{}, time.UTC)
	require.NoError(t, err)
	return sched
}

func TestExecuteBlocks_CreateTask(t *testing.T) {
	s := setupTestScheduler(t)
	s.Start()
	defer s.Shutdown()

	text := "I'll set that up.\n```nclaw:schedule\n" +
		`{"action":"create","prompt":"check weather","type":"interval","value":"1h"}` +
		"\n```\nDone!"
	errMsg := s.ExecuteBlocks(text, 100, 5)
	assert.Empty(t, errMsg)

	display := blocks.StripAll(text)
	assert.Contains(t, display, "I'll set that up.")
	assert.Contains(t, display, "Done!")
	assert.NotContains(t, display, "nclaw:schedule")

	// Verify task was actually created in DB
	var tasks []model.ScheduledTask
	require.NoError(t, s.db.Find(&tasks).Error)
	assert.Len(t, tasks, 1)
	assert.Equal(t, "check weather", tasks[0].Prompt)
	assert.Equal(t, model.ScheduleInterval, tasks[0].ScheduleType)
	assert.Equal(t, "1h", tasks[0].ScheduleValue)
	assert.Equal(t, int64(100), tasks[0].ChatID)
	assert.Equal(t, 5, tasks[0].ThreadID)
}

func TestExecuteBlocks_CreateTaskMissingFields(t *testing.T) {
	s := setupTestScheduler(t)
	text := "```nclaw:schedule\n{\"action\":\"create\",\"prompt\":\"\"}\n```"
	result := s.ExecuteBlocks(text, 100, 0)
	assert.Contains(t, result, "Ошибка расписания")
}

func TestExecuteBlocks_InvalidJSON(t *testing.T) {
	s := setupTestScheduler(t)
	text := "```nclaw:schedule\n{invalid json}\n```"
	result := s.ExecuteBlocks(text, 100, 0)
	assert.Contains(t, result, "Ошибка расписания")
}

func TestExecuteBlocks_UnknownAction(t *testing.T) {
	s := setupTestScheduler(t)
	text := "```nclaw:schedule\n{\"action\":\"explode\"}\n```"
	result := s.ExecuteBlocks(text, 100, 0)
	assert.Contains(t, result, "Ошибка расписания")
	assert.Contains(t, result, "unknown action")
}

func TestExecuteBlocks_CreateWithContextMode(t *testing.T) {
	s := setupTestScheduler(t)
	s.Start()
	defer s.Shutdown()

	text := "```nclaw:schedule\n" +
		`{"action":"create","prompt":"check","type":"interval","value":"30m","context":"isolated"}` +
		"\n```"
	result := s.ExecuteBlocks(text, 200, 0)
	assert.Empty(t, result)

	var tasks []model.ScheduledTask
	require.NoError(t, s.db.Find(&tasks).Error)
	assert.Equal(t, model.ContextIsolated, tasks[0].ContextMode)
}

func TestExecuteBlocks_DefaultContextMode(t *testing.T) {
	s := setupTestScheduler(t)
	s.Start()
	defer s.Shutdown()

	text := "```nclaw:schedule\n" +
		`{"action":"create","prompt":"check","type":"interval","value":"1h"}` +
		"\n```"
	s.ExecuteBlocks(text, 200, 0)

	var tasks []model.ScheduledTask
	require.NoError(t, s.db.Find(&tasks).Error)
	assert.Equal(t, model.ContextGroup, tasks[0].ContextMode)
}

func TestExecuteBlocks_CreateNotifyTask(t *testing.T) {
	s := setupTestScheduler(t)
	s.Start()
	defer s.Shutdown()

	text := "```nclaw:schedule\n" +
		`{"action":"create","prompt":"⏰ Call the dentist","type":"interval","value":"1h","context":"notify"}` +
		"\n```"
	assert.Empty(t, s.ExecuteBlocks(text, 200, 0))

	var tasks []model.ScheduledTask
	require.NoError(t, s.db.Find(&tasks).Error)
	require.Len(t, tasks, 1)
	assert.Equal(t, model.ContextNotify, tasks[0].ContextMode)
	assert.Contains(t, FormatTaskList(s.db, time.UTC, 200, 0), "context=notify")
}

func TestExecuteBlocks_RejectsUnknownContext(t *testing.T) {
	s := setupTestScheduler(t)
	s.Start()
	defer s.Shutdown()

	text := "```nclaw:schedule\n" +
		`{"action":"create","prompt":"check","type":"interval","value":"1h","context":"silent"}` +
		"\n```"
	assert.Contains(t, s.ExecuteBlocks(text, 200, 0), `invalid context "silent"`)

	var count int64
	require.NoError(t, s.db.Model(&model.ScheduledTask{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestExecuteBlocks_NoBlocks(t *testing.T) {
	s := setupTestScheduler(t)
	result := s.ExecuteBlocks("plain text", 100, 0)
	assert.Empty(t, result)
}

func TestExecuteBlocks_Success(t *testing.T) {
	s := setupTestScheduler(t)
	s.Start()
	defer s.Shutdown()

	text := "text\n```nclaw:schedule\n" +
		`{"action":"create","prompt":"check weather","type":"interval","value":"1h"}` +
		"\n```\nmore"
	result := s.ExecuteBlocks(text, 100, 0)
	assert.Empty(t, result)

	var tasks []model.ScheduledTask
	require.NoError(t, s.db.Find(&tasks).Error)
	assert.Len(t, tasks, 1)
	assert.Equal(t, "check weather", tasks[0].Prompt)
}

func TestExecuteBlocks_Error(t *testing.T) {
	s := setupTestScheduler(t)
	text := "```nclaw:schedule\n{invalid json}\n```"
	result := s.ExecuteBlocks(text, 100, 0)
	assert.Contains(t, result, "[Ошибка расписания:")
}

func TestExecuteBlocks_MixedSuccessAndError(t *testing.T) {
	s := setupTestScheduler(t)
	s.Start()
	defer s.Shutdown()

	text := "```nclaw:schedule\n" +
		`{"action":"create","prompt":"ok","type":"interval","value":"1h"}` +
		"\n```\n```nclaw:schedule\n{bad}\n```"
	result := s.ExecuteBlocks(text, 100, 0)
	assert.Contains(t, result, "[Ошибка расписания:")

	var tasks []model.ScheduledTask
	require.NoError(t, s.db.Find(&tasks).Error)
	assert.Len(t, tasks, 1)
}

func TestTruncate(t *testing.T) {
	assert.Equal(t, "hello", truncate("hello", 10))
	assert.Equal(t, "hello", truncate("hello", 5))
	assert.Equal(t, "hel", truncate("hello", 3))
	assert.Equal(t, "", truncate("", 5))
}

func TestFormatTaskList_Empty(t *testing.T) {
	s := setupTestScheduler(t)
	result := FormatTaskList(s.db, time.UTC, 100, 0)
	assert.Equal(t, "Current scheduled tasks: none", result)
}

func TestFormatTaskList_WithTasks(t *testing.T) {
	s := setupTestScheduler(t)

	now := time.Now().Add(time.Hour)
	task := &model.ScheduledTask{
		ID:            "task-123",
		ChatID:        100,
		ThreadID:      0,
		Prompt:        "check weather",
		ScheduleType:  model.ScheduleInterval,
		ScheduleValue: "1h",
		ContextMode:   model.ContextGroup,
		Status:        model.StatusActive,
		NextRun:       &now,
		CreatedAt:     time.Now(),
	}
	require.NoError(t, s.db.Create(task).Error)

	result := FormatTaskList(s.db, time.UTC, 100, 0)
	assert.Contains(t, result, "Current scheduled tasks:")
	assert.Contains(t, result, "task-123")
	assert.Contains(t, result, "check weather")
	assert.Contains(t, result, "interval")
	assert.Contains(t, result, "active")
}

func TestFormatTaskList_DifferentChat(t *testing.T) {
	s := setupTestScheduler(t)

	task := &model.ScheduledTask{
		ID:            "task-456",
		ChatID:        999,
		ThreadID:      0,
		Prompt:        "other task",
		ScheduleType:  model.ScheduleCron,
		ScheduleValue: "0 9 * * *",
		ContextMode:   model.ContextGroup,
		Status:        model.StatusActive,
		CreatedAt:     time.Now(),
	}
	require.NoError(t, s.db.Create(task).Error)

	// Should not appear for chat 100
	result := FormatTaskList(s.db, time.UTC, 100, 0)
	assert.Equal(t, "Current scheduled tasks: none", result)
}

func TestExecuteBlocks_TaskActionsRequireOwner(t *testing.T) {
	cases := []struct {
		name     string
		chatID   int64
		threadID int
	}{
		{"other chat", 200, 0},
		{"other thread", 100, 5},
	}
	for _, tc := range cases {
		for _, action := range []string{"pause", "resume", "cancel"} {
			t.Run(tc.name+"/"+action, func(t *testing.T) {
				s := setupTestScheduler(t)
				task := &model.ScheduledTask{
					ID: model.GenerateTaskID(), ChatID: 100, Prompt: "p",
					ScheduleType: model.ScheduleInterval, ScheduleValue: "1h",
					ContextMode: model.ContextGroup, Status: model.StatusPaused, CreatedAt: time.Now(),
				}
				require.NoError(t, s.db.Create(task).Error)

				block := "```nclaw:schedule\n{\"action\":\"" + action + "\",\"task_id\":\"" + task.ID + "\"}\n```"
				result := s.ExecuteBlocks(block, tc.chatID, tc.threadID)
				assert.Contains(t, result, "task not found: "+task.ID)

				var got model.ScheduledTask
				require.NoError(t, s.db.First(&got, "id = ?", task.ID).Error)
				assert.Equal(t, model.StatusPaused, got.Status)
			})
		}
	}
}

func TestExecuteBlocks_TaskActionByOwner(t *testing.T) {
	s := setupTestScheduler(t)
	s.Start()
	defer s.Shutdown()

	task := &model.ScheduledTask{
		ID: model.GenerateTaskID(), ChatID: 100, ThreadID: 5, Prompt: "p",
		ScheduleType: model.ScheduleInterval, ScheduleValue: "1h",
		ContextMode: model.ContextGroup, Status: model.StatusActive, CreatedAt: time.Now(),
	}
	require.NoError(t, s.CreateTask(task))

	block := "```nclaw:schedule\n{\"action\":\"pause\",\"task_id\":\"" + task.ID + "\"}\n```"
	assert.Empty(t, s.ExecuteBlocks(block, 100, 5))

	var got model.ScheduledTask
	require.NoError(t, s.db.First(&got, "id = ?", task.ID).Error)
	assert.Equal(t, model.StatusPaused, got.Status)
}

func TestExecuteBlocks_TaskActionUnknownTask(t *testing.T) {
	s := setupTestScheduler(t)
	result := s.ExecuteBlocks("```nclaw:schedule\n{\"action\":\"cancel\",\"task_id\":\"task-missing\"}\n```", 100, 0)
	assert.Contains(t, result, "task not found: task-missing")
}

func TestFormatTaskList_HidesFinishedTasks(t *testing.T) {
	s := setupTestScheduler(t)
	for id, status := range map[string]string{
		"task-active": model.StatusActive, "task-paused": model.StatusPaused,
		"task-done": model.StatusCompleted, "task-failed": model.StatusFailed,
	} {
		require.NoError(t, s.db.Create(&model.ScheduledTask{
			ID: id, ChatID: 100, Prompt: "p", ScheduleType: model.ScheduleOnce, ScheduleValue: "2026-01-01T00:00:00",
			ContextMode: model.ContextGroup, Status: status, CreatedAt: time.Now(),
		}).Error)
	}

	result := FormatTaskList(s.db, time.UTC, 100, 0)

	assert.Contains(t, result, "task-active")
	assert.Contains(t, result, "task-paused")
	assert.NotContains(t, result, "task-done")
	assert.NotContains(t, result, "task-failed")
}
