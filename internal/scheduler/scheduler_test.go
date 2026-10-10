package scheduler

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/nickalie/nclaw/internal/buttons"
	"github.com/nickalie/nclaw/internal/chatqueue"
	"github.com/nickalie/nclaw/internal/cli"
	"github.com/nickalie/nclaw/internal/db"
	"github.com/nickalie/nclaw/internal/invoker"
	"github.com/nickalie/nclaw/internal/model"
	"github.com/nickalie/nclaw/internal/pipeline"
	"github.com/nickalie/nclaw/internal/sendfile"
	"github.com/nickalie/nclaw/internal/telegram"
)

func TestLoadTasks_OnceTaskPastTime(t *testing.T) {
	s := setupTestScheduler(t)
	s.Start()
	defer s.Shutdown()

	past := time.Now().Add(-time.Hour).UTC().Format("2006-01-02T15:04:05")
	task := &model.ScheduledTask{
		ID:            "task-past-once",
		ChatID:        100,
		Prompt:        "overdue task",
		ScheduleType:  model.ScheduleOnce,
		ScheduleValue: past,
		ContextMode:   model.ContextIsolated,
		Status:        model.StatusActive,
		CreatedAt:     time.Now(),
	}
	require.NoError(t, s.db.Create(task).Error)

	s.LoadTasks()

	s.mu.Lock()
	_, registered := s.jobs[task.ID]
	s.mu.Unlock()
	assert.True(t, registered, "once task with past time should be registered in gocron")
}

func TestLoadTasks_OnceTaskFutureTime(t *testing.T) {
	s := setupTestScheduler(t)
	s.Start()
	defer s.Shutdown()

	future := time.Now().Add(time.Hour).UTC().Format("2006-01-02T15:04:05")
	task := &model.ScheduledTask{
		ID:            "task-future-once",
		ChatID:        100,
		Prompt:        "future task",
		ScheduleType:  model.ScheduleOnce,
		ScheduleValue: future,
		ContextMode:   model.ContextIsolated,
		Status:        model.StatusActive,
		CreatedAt:     time.Now(),
	}
	require.NoError(t, s.db.Create(task).Error)

	s.LoadTasks()

	s.mu.Lock()
	_, registered := s.jobs[task.ID]
	s.mu.Unlock()
	assert.True(t, registered, "once task with future time should be registered in gocron")
}

func TestLoadTasks_SkipsPausedTasks(t *testing.T) {
	s := setupTestScheduler(t)
	s.Start()
	defer s.Shutdown()

	task := &model.ScheduledTask{
		ID:            "task-paused",
		ChatID:        100,
		Prompt:        "paused task",
		ScheduleType:  model.ScheduleInterval,
		ScheduleValue: "1h",
		ContextMode:   model.ContextGroup,
		Status:        model.StatusPaused,
		CreatedAt:     time.Now(),
	}
	require.NoError(t, s.db.Create(task).Error)

	s.LoadTasks()

	s.mu.Lock()
	_, registered := s.jobs[task.ID]
	s.mu.Unlock()
	assert.False(t, registered, "paused task should not be loaded")
}

func TestLoadTasks_MixedTasks(t *testing.T) {
	s := setupTestScheduler(t)
	s.Start()
	defer s.Shutdown()

	past := time.Now().Add(-time.Hour).UTC().Format("2006-01-02T15:04:05")
	tasks := []model.ScheduledTask{
		{
			ID: "task-cron", ChatID: 100, Prompt: "cron task",
			ScheduleType: model.ScheduleCron, ScheduleValue: "0 12 * * *",
			ContextMode: model.ContextGroup, Status: model.StatusActive, CreatedAt: time.Now(),
		},
		{
			ID: "task-interval", ChatID: 100, Prompt: "interval task",
			ScheduleType: model.ScheduleInterval, ScheduleValue: "30m",
			ContextMode: model.ContextGroup, Status: model.StatusActive, CreatedAt: time.Now(),
		},
		{
			ID: "task-once-past", ChatID: 100, Prompt: "overdue once",
			ScheduleType: model.ScheduleOnce, ScheduleValue: past,
			ContextMode: model.ContextIsolated, Status: model.StatusActive, CreatedAt: time.Now(),
		},
		{
			ID: "task-completed", ChatID: 100, Prompt: "done",
			ScheduleType: model.ScheduleInterval, ScheduleValue: "1h",
			ContextMode: model.ContextGroup, Status: model.StatusCompleted, CreatedAt: time.Now(),
		},
	}
	for i := range tasks {
		require.NoError(t, s.db.Create(&tasks[i]).Error)
	}

	s.LoadTasks()

	s.mu.Lock()
	defer s.mu.Unlock()
	assert.Contains(t, s.jobs, "task-cron")
	assert.Contains(t, s.jobs, "task-interval")
	assert.Contains(t, s.jobs, "task-once-past", "once task with past time should load via immediate mode")
	assert.NotContains(t, s.jobs, "task-completed", "completed task should not be loaded")
}

// --- PauseTask tests ---

func TestPauseTask_Active(t *testing.T) {
	s := setupTestScheduler(t)
	s.Start()
	defer s.Shutdown()

	task := &model.ScheduledTask{
		ID:            "task-pause-active",
		ChatID:        100,
		Prompt:        "pause me",
		ScheduleType:  model.ScheduleInterval,
		ScheduleValue: "1h",
		ContextMode:   model.ContextGroup,
		Status:        model.StatusActive,
		CreatedAt:     time.Now(),
	}
	require.NoError(t, s.db.Create(task).Error)
	require.NoError(t, s.addJob(task))

	err := s.PauseTask(task.ID)
	require.NoError(t, err)

	// Verify DB status is paused
	var updated model.ScheduledTask
	require.NoError(t, s.db.First(&updated, "id = ?", task.ID).Error)
	assert.Equal(t, model.StatusPaused, updated.Status)

	// Verify job removed from scheduler
	s.mu.Lock()
	_, registered := s.jobs[task.ID]
	s.mu.Unlock()
	assert.False(t, registered, "paused task should be removed from jobs map")
}

func TestPauseTask_AlreadyPaused(t *testing.T) {
	s := setupTestScheduler(t)

	task := &model.ScheduledTask{
		ID:            "task-pause-paused",
		ChatID:        100,
		Prompt:        "already paused",
		ScheduleType:  model.ScheduleInterval,
		ScheduleValue: "1h",
		ContextMode:   model.ContextGroup,
		Status:        model.StatusPaused,
		CreatedAt:     time.Now(),
	}
	require.NoError(t, s.db.Create(task).Error)

	err := s.PauseTask(task.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not active")
}

func TestPauseTask_NonExistent(t *testing.T) {
	s := setupTestScheduler(t)

	err := s.PauseTask("no-such-task")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get task")
}

// --- ResumeTask tests ---

func TestResumeTask_Paused(t *testing.T) {
	s := setupTestScheduler(t)
	s.Start()
	defer s.Shutdown()

	task := &model.ScheduledTask{
		ID:            "task-resume-paused",
		ChatID:        100,
		Prompt:        "resume me",
		ScheduleType:  model.ScheduleInterval,
		ScheduleValue: "1h",
		ContextMode:   model.ContextGroup,
		Status:        model.StatusPaused,
		CreatedAt:     time.Now(),
	}
	require.NoError(t, s.db.Create(task).Error)

	err := s.ResumeTask(task.ID)
	require.NoError(t, err)

	// Verify DB status is active
	var updated model.ScheduledTask
	require.NoError(t, s.db.First(&updated, "id = ?", task.ID).Error)
	assert.Equal(t, model.StatusActive, updated.Status)

	// Verify job registered in scheduler
	s.mu.Lock()
	_, registered := s.jobs[task.ID]
	s.mu.Unlock()
	assert.True(t, registered, "resumed task should be registered in jobs map")
}

func TestResumeTask_AlreadyActive(t *testing.T) {
	s := setupTestScheduler(t)
	s.Start()
	defer s.Shutdown()

	task := &model.ScheduledTask{
		ID:            "task-resume-active",
		ChatID:        100,
		Prompt:        "already active",
		ScheduleType:  model.ScheduleInterval,
		ScheduleValue: "1h",
		ContextMode:   model.ContextGroup,
		Status:        model.StatusActive,
		CreatedAt:     time.Now(),
	}
	require.NoError(t, s.db.Create(task).Error)

	err := s.ResumeTask(task.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not paused")
}

func TestResumeTask_NonExistent(t *testing.T) {
	s := setupTestScheduler(t)

	err := s.ResumeTask("no-such-task")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get task")
}

// --- CancelTask tests ---

func TestCancelTask_Active(t *testing.T) {
	s := setupTestScheduler(t)
	s.Start()
	defer s.Shutdown()

	task := &model.ScheduledTask{
		ID:            "task-cancel-active",
		ChatID:        100,
		Prompt:        "cancel me",
		ScheduleType:  model.ScheduleInterval,
		ScheduleValue: "1h",
		ContextMode:   model.ContextGroup,
		Status:        model.StatusActive,
		CreatedAt:     time.Now(),
	}
	require.NoError(t, s.db.Create(task).Error)
	require.NoError(t, s.addJob(task))

	err := s.CancelTask(task.ID)
	require.NoError(t, err)

	// Verify task deleted from DB
	var count int64
	s.db.Model(&model.ScheduledTask{}).Where("id = ?", task.ID).Count(&count)
	assert.Equal(t, int64(0), count, "canceled task should be deleted from DB")

	// Verify job removed from scheduler
	s.mu.Lock()
	_, registered := s.jobs[task.ID]
	s.mu.Unlock()
	assert.False(t, registered, "canceled task should be removed from jobs map")
}

func TestCancelTask_NonExistent(t *testing.T) {
	s := setupTestScheduler(t)

	// CancelTask calls db.DeleteTask which does not error on missing rows
	// (DELETE WHERE is a no-op), so this should succeed
	err := s.CancelTask("no-such-task")
	require.NoError(t, err)
}

// --- CreateTask tests ---

func TestCreateTask_Interval(t *testing.T) {
	s := setupTestScheduler(t)
	s.Start()
	defer s.Shutdown()

	task := &model.ScheduledTask{
		ID:            "task-create-interval",
		ChatID:        200,
		ThreadID:      1,
		Prompt:        "check status",
		ScheduleType:  model.ScheduleInterval,
		ScheduleValue: "30m",
		ContextMode:   model.ContextGroup,
		Status:        model.StatusActive,
		CreatedAt:     time.Now(),
	}

	err := s.CreateTask(task)
	require.NoError(t, err)

	// Verify persisted in DB
	var stored model.ScheduledTask
	require.NoError(t, s.db.First(&stored, "id = ?", task.ID).Error)
	assert.Equal(t, "check status", stored.Prompt)
	assert.Equal(t, model.ScheduleInterval, stored.ScheduleType)
	assert.Equal(t, "30m", stored.ScheduleValue)
	assert.Equal(t, int64(200), stored.ChatID)
	assert.Equal(t, 1, stored.ThreadID)

	// Verify registered in gocron
	s.mu.Lock()
	_, registered := s.jobs[task.ID]
	s.mu.Unlock()
	assert.True(t, registered)
}

func TestCreateTask_Cron(t *testing.T) {
	s := setupTestScheduler(t)
	s.Start()
	defer s.Shutdown()

	task := &model.ScheduledTask{
		ID:            "task-create-cron",
		ChatID:        200,
		Prompt:        "daily report",
		ScheduleType:  model.ScheduleCron,
		ScheduleValue: "0 9 * * *",
		ContextMode:   model.ContextIsolated,
		Status:        model.StatusActive,
		CreatedAt:     time.Now(),
	}

	err := s.CreateTask(task)
	require.NoError(t, err)

	var stored model.ScheduledTask
	require.NoError(t, s.db.First(&stored, "id = ?", task.ID).Error)
	assert.Equal(t, model.ScheduleCron, stored.ScheduleType)
	assert.Equal(t, model.ContextIsolated, stored.ContextMode)

	s.mu.Lock()
	_, registered := s.jobs[task.ID]
	s.mu.Unlock()
	assert.True(t, registered)
}

func TestCreateTask_Once(t *testing.T) {
	s := setupTestScheduler(t)
	s.Start()
	defer s.Shutdown()

	future := time.Now().Add(2 * time.Hour).UTC().Format("2006-01-02T15:04:05")
	task := &model.ScheduledTask{
		ID:            "task-create-once",
		ChatID:        200,
		Prompt:        "one-time reminder",
		ScheduleType:  model.ScheduleOnce,
		ScheduleValue: future,
		ContextMode:   model.ContextGroup,
		Status:        model.StatusActive,
		CreatedAt:     time.Now(),
	}

	err := s.CreateTask(task)
	require.NoError(t, err)

	var stored model.ScheduledTask
	require.NoError(t, s.db.First(&stored, "id = ?", task.ID).Error)
	assert.Equal(t, model.ScheduleOnce, stored.ScheduleType)

	s.mu.Lock()
	_, registered := s.jobs[task.ID]
	s.mu.Unlock()
	assert.True(t, registered)
}

// --- jobDefinition tests ---

func TestJobDefinition_Cron(t *testing.T) {
	s := setupTestScheduler(t)

	task := &model.ScheduledTask{
		ScheduleType:  model.ScheduleCron,
		ScheduleValue: "*/5 * * * *",
	}
	def, err := s.jobDefinition(task)
	require.NoError(t, err)
	assert.NotNil(t, def)
}

func TestJobDefinition_Interval(t *testing.T) {
	s := setupTestScheduler(t)

	task := &model.ScheduledTask{
		ScheduleType:  model.ScheduleInterval,
		ScheduleValue: "45m",
	}
	def, err := s.jobDefinition(task)
	require.NoError(t, err)
	assert.NotNil(t, def)
}

func TestJobDefinition_IntervalInvalid(t *testing.T) {
	s := setupTestScheduler(t)

	task := &model.ScheduledTask{
		ScheduleType:  model.ScheduleInterval,
		ScheduleValue: "not-a-duration",
	}
	_, err := s.jobDefinition(task)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse interval")
}

func TestJobDefinition_OnceFuture(t *testing.T) {
	s := setupTestScheduler(t)

	future := time.Now().Add(time.Hour).UTC().Format("2006-01-02T15:04:05")
	task := &model.ScheduledTask{
		ID:            "task-def-once-future",
		ScheduleType:  model.ScheduleOnce,
		ScheduleValue: future,
	}
	def, err := s.jobDefinition(task)
	require.NoError(t, err)
	assert.NotNil(t, def)
}

func TestJobDefinition_OncePast(t *testing.T) {
	s := setupTestScheduler(t)

	past := time.Now().Add(-time.Hour).UTC().Format("2006-01-02T15:04:05")
	task := &model.ScheduledTask{
		ID:            "task-def-once-past",
		ScheduleType:  model.ScheduleOnce,
		ScheduleValue: past,
	}
	def, err := s.jobDefinition(task)
	require.NoError(t, err)
	assert.NotNil(t, def, "past once task should return immediate job definition")
}

func TestJobDefinition_OnceInvalidTime(t *testing.T) {
	s := setupTestScheduler(t)

	task := &model.ScheduledTask{
		ScheduleType:  model.ScheduleOnce,
		ScheduleValue: "not-a-time",
	}
	_, err := s.jobDefinition(task)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse once time")
}

func TestJobDefinition_UnknownType(t *testing.T) {
	s := setupTestScheduler(t)

	task := &model.ScheduledTask{
		ScheduleType:  "lunar",
		ScheduleValue: "full-moon",
	}
	_, err := s.jobDefinition(task)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown schedule type")
}

// --- Start/Shutdown lifecycle test ---

func TestStartShutdown(t *testing.T) {
	s := setupTestScheduler(t)

	// Start should not panic
	s.Start()

	// Create a task while running to verify scheduler is functional
	task := &model.ScheduledTask{
		ID:            "task-lifecycle",
		ChatID:        100,
		Prompt:        "lifecycle test",
		ScheduleType:  model.ScheduleInterval,
		ScheduleValue: "1h",
		ContextMode:   model.ContextGroup,
		Status:        model.StatusActive,
		CreatedAt:     time.Now(),
	}
	err := s.CreateTask(task)
	require.NoError(t, err)

	s.mu.Lock()
	_, registered := s.jobs[task.ID]
	s.mu.Unlock()
	assert.True(t, registered)

	// Shutdown should not error
	err = s.Shutdown()
	require.NoError(t, err)
}

// --- Pause then Resume round-trip ---

func TestPauseThenResume(t *testing.T) {
	s := setupTestScheduler(t)
	s.Start()
	defer s.Shutdown()

	task := &model.ScheduledTask{
		ID:            "task-pause-resume",
		ChatID:        100,
		Prompt:        "round trip",
		ScheduleType:  model.ScheduleInterval,
		ScheduleValue: "2h",
		ContextMode:   model.ContextGroup,
		Status:        model.StatusActive,
		CreatedAt:     time.Now(),
	}
	require.NoError(t, s.db.Create(task).Error)
	require.NoError(t, s.addJob(task))

	// Pause
	require.NoError(t, s.PauseTask(task.ID))
	s.mu.Lock()
	_, registered := s.jobs[task.ID]
	s.mu.Unlock()
	assert.False(t, registered, "job should be removed after pause")

	// Resume
	require.NoError(t, s.ResumeTask(task.ID))
	s.mu.Lock()
	_, registered = s.jobs[task.ID]
	s.mu.Unlock()
	assert.True(t, registered, "job should be re-registered after resume")

	// Verify DB status is active again
	var updated model.ScheduledTask
	require.NoError(t, s.db.First(&updated, "id = ?", task.ID).Error)
	assert.Equal(t, model.StatusActive, updated.Status)
}

// --- Mock CLI client and provider for execution tests ---

type mockCLIClient struct{}

func (m *mockCLIClient) Dir(_ string) cli.Client                { return m }
func (m *mockCLIClient) Context(context.Context) cli.Client     { return m }
func (m *mockCLIClient) SkipPermissions() cli.Client            { return m }
func (m *mockCLIClient) AppendSystemPrompt(_ string) cli.Client { return m }

func (m *mockCLIClient) Ask(_ string) (*cli.Result, error) {
	return &cli.Result{Text: "mock reply", FullText: "mock full reply"}, nil
}

func (m *mockCLIClient) Continue(_ string) (*cli.Result, error) {
	return &cli.Result{Text: "mock reply", FullText: "mock full reply"}, nil
}

type mockCLIProvider struct{}

func (m *mockCLIProvider) NewClient() cli.Client    { return &mockCLIClient{} }
func (m *mockCLIProvider) PreInvoke() error         { return nil }
func (m *mockCLIProvider) Version() (string, error) { return "mock-cli-1.0.0", nil }
func (m *mockCLIProvider) Name() string             { return "mock-cli" }

func setupTestSchedulerWithMockCLI(t *testing.T) *Scheduler {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(&model.ScheduledTask{}, &model.TaskRunLog{}))

	sched, err := New(database, invoker.New(&mockCLIProvider{}, invoker.Options{DataDir: t.TempDir()}), inlineRunner{}, time.UTC)
	require.NoError(t, err)
	return sched
}

// --- Execution path tests ---

func TestSetPipeline(t *testing.T) {
	s := setupTestScheduler(t)
	assert.Nil(t, s.pipeline)

	p := &pipeline.Pipeline{}
	s.SetPipeline(p)
	assert.Equal(t, p, s.pipeline)
}

func TestExecuteTask_Success(t *testing.T) {
	s := setupTestSchedulerWithMockCLI(t)
	s.Start()
	defer s.Shutdown()

	task := &model.ScheduledTask{
		ID:            "exec-success",
		ChatID:        100,
		Prompt:        "do something",
		ScheduleType:  model.ScheduleInterval,
		ScheduleValue: "1h",
		ContextMode:   model.ContextGroup,
		Status:        model.StatusActive,
		CreatedAt:     time.Now(),
	}
	require.NoError(t, s.db.Create(task).Error)

	s.executeTask(task.ID)

	// Verify TaskRunLog was created with status=success
	var logs []model.TaskRunLog
	require.NoError(t, s.db.Where("task_id = ?", task.ID).Find(&logs).Error)
	require.Len(t, logs, 1)
	assert.Equal(t, "success", logs[0].Status)
	assert.Nil(t, logs[0].Error)
	assert.NotNil(t, logs[0].Result)
}

func TestExecuteTask_TaskNotFound(t *testing.T) {
	s := setupTestSchedulerWithMockCLI(t)

	// Should not panic; just returns without doing anything
	s.executeTask("nonexistent-task")

	// Verify no run logs were created
	var count int64
	s.db.Model(&model.TaskRunLog{}).Count(&count)
	assert.Equal(t, int64(0), count)
}

func TestExecuteTask_InactiveTask(t *testing.T) {
	s := setupTestSchedulerWithMockCLI(t)

	task := &model.ScheduledTask{
		ID:            "exec-inactive",
		ChatID:        100,
		Prompt:        "paused task",
		ScheduleType:  model.ScheduleInterval,
		ScheduleValue: "1h",
		ContextMode:   model.ContextGroup,
		Status:        model.StatusPaused,
		CreatedAt:     time.Now(),
	}
	require.NoError(t, s.db.Create(task).Error)

	s.executeTask(task.ID)

	// Verify no run was logged
	var count int64
	s.db.Model(&model.TaskRunLog{}).Where("task_id = ?", task.ID).Count(&count)
	assert.Equal(t, int64(0), count)
}

func TestRecordResults_Success(t *testing.T) {
	s := setupTestSchedulerWithMockCLI(t)
	s.Start()
	defer s.Shutdown()

	task := &model.ScheduledTask{
		ID:            "record-success",
		ChatID:        100,
		Prompt:        "test task",
		ScheduleType:  model.ScheduleInterval,
		ScheduleValue: "1h",
		ContextMode:   model.ContextGroup,
		Status:        model.StatusActive,
		CreatedAt:     time.Now(),
	}
	require.NoError(t, s.db.Create(task).Error)
	require.NoError(t, s.addJob(task))

	err := s.recordResults(task, "some reply", nil, 5*time.Second)
	require.NoError(t, err)

	// Verify TaskRunLog was created
	var logs []model.TaskRunLog
	require.NoError(t, s.db.Where("task_id = ?", task.ID).Find(&logs).Error)
	require.Len(t, logs, 1)
	assert.Equal(t, "success", logs[0].Status)

	// Verify task was updated
	var updated model.ScheduledTask
	require.NoError(t, s.db.First(&updated, "id = ?", task.ID).Error)
	assert.Contains(t, *updated.LastResult, "some reply")
}

func TestRecordResults_Error(t *testing.T) {
	s := setupTestSchedulerWithMockCLI(t)
	s.Start()
	defer s.Shutdown()

	task := &model.ScheduledTask{
		ID:            "record-error",
		ChatID:        100,
		Prompt:        "once task",
		ScheduleType:  model.ScheduleOnce,
		ScheduleValue: time.Now().Add(time.Hour).UTC().Format("2006-01-02T15:04:05"),
		ContextMode:   model.ContextIsolated,
		Status:        model.StatusActive,
		CreatedAt:     time.Now(),
	}
	require.NoError(t, s.db.Create(task).Error)
	require.NoError(t, s.addJob(task))

	runErr := errors.New("cli failed")
	err := s.recordResults(task, "", runErr, 2*time.Second)
	require.NoError(t, err)

	// Verify task status became "failed" for once task with error
	var updated model.ScheduledTask
	require.NoError(t, s.db.First(&updated, "id = ?", task.ID).Error)
	assert.Equal(t, model.StatusFailed, updated.Status)
}

func TestRecordResults_DeletedTask(t *testing.T) {
	s := setupTestSchedulerWithMockCLI(t)

	task := &model.ScheduledTask{
		ID:            "record-deleted",
		ChatID:        100,
		Prompt:        "gone task",
		ScheduleType:  model.ScheduleInterval,
		ScheduleValue: "1h",
		ContextMode:   model.ContextGroup,
		Status:        model.StatusActive,
		CreatedAt:     time.Now(),
	}
	// Don't create the task in DB — it's "deleted"

	err := s.recordResults(task, "reply", nil, time.Second)
	require.Error(t, err)
	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound))
}

func TestLogRunTx_Success(t *testing.T) {
	s := setupTestSchedulerWithMockCLI(t)

	// Create a task first so foreign key (if any) is satisfied
	task := &model.ScheduledTask{
		ID:            "logrun-success",
		ChatID:        100,
		Prompt:        "test",
		ScheduleType:  model.ScheduleInterval,
		ScheduleValue: "1h",
		ContextMode:   model.ContextGroup,
		Status:        model.StatusActive,
		CreatedAt:     time.Now(),
	}
	require.NoError(t, s.db.Create(task).Error)

	err := s.logRunTx(s.db, task.ID, "reply text", nil, 3*time.Second)
	require.NoError(t, err)

	var logs []model.TaskRunLog
	require.NoError(t, s.db.Where("task_id = ?", task.ID).Find(&logs).Error)
	require.Len(t, logs, 1)
	assert.Equal(t, "success", logs[0].Status)
	assert.Nil(t, logs[0].Error)
	assert.NotNil(t, logs[0].Result)
	assert.Equal(t, "reply text", *logs[0].Result)
}

func TestLogRunTx_Error(t *testing.T) {
	s := setupTestSchedulerWithMockCLI(t)

	task := &model.ScheduledTask{
		ID:            "logrun-error",
		ChatID:        100,
		Prompt:        "test",
		ScheduleType:  model.ScheduleInterval,
		ScheduleValue: "1h",
		ContextMode:   model.ContextGroup,
		Status:        model.StatusActive,
		CreatedAt:     time.Now(),
	}
	require.NoError(t, s.db.Create(task).Error)

	runErr := errors.New("something went wrong")
	err := s.logRunTx(s.db, task.ID, "", runErr, 2*time.Second)
	require.NoError(t, err)

	var logs []model.TaskRunLog
	require.NoError(t, s.db.Where("task_id = ?", task.ID).Find(&logs).Error)
	require.Len(t, logs, 1)
	assert.Equal(t, "error", logs[0].Status)
	require.NotNil(t, logs[0].Error)
	assert.Equal(t, "something went wrong", *logs[0].Error)
}

func TestResolveNextRun_OnceTask(t *testing.T) {
	s := setupTestSchedulerWithMockCLI(t)

	task := &model.ScheduledTask{
		ID:           "resolve-once",
		ScheduleType: model.ScheduleOnce,
	}

	result := s.resolveNextRun(task)
	assert.Nil(t, result, "once tasks should return nil next run")
}

func TestResolveNextRun_IntervalTask(t *testing.T) {
	s := setupTestSchedulerWithMockCLI(t)
	s.Start()
	defer s.Shutdown()

	task := &model.ScheduledTask{
		ID:            "resolve-interval",
		ChatID:        100,
		Prompt:        "test",
		ScheduleType:  model.ScheduleInterval,
		ScheduleValue: "1h",
		ContextMode:   model.ContextGroup,
		Status:        model.StatusActive,
		CreatedAt:     time.Now(),
	}
	require.NoError(t, s.db.Create(task).Error)
	require.NoError(t, s.addJob(task))

	result := s.resolveNextRun(task)
	assert.NotNil(t, result, "interval task with registered job should have a next run time")
}

func TestGetNextRun_NoJobs(t *testing.T) {
	s := setupTestSchedulerWithMockCLI(t)
	s.Start()
	defer s.Shutdown()

	// Use a random UUID that doesn't match any job
	fakeID := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	result := s.getNextRun(fakeID)
	assert.Nil(t, result, "should return nil for unknown job ID")
}

func TestSendResult_NilPipeline(t *testing.T) {
	s := setupTestSchedulerWithMockCLI(t)
	// pipeline is nil by default

	task := &model.ScheduledTask{
		ID:       "send-nil-pipeline",
		ChatID:   100,
		ThreadID: 0,
	}
	result := &cli.Result{Text: "hello", FullText: "hello"}

	// Should not panic
	assert.NotPanics(t, func() {
		s.sendResult(task, result, nil, t.TempDir())
	})
}

func TestClearRunState(t *testing.T) {
	s := setupTestSchedulerWithMockCLI(t)

	taskID := "clear-state-test"

	s.mu.Lock()
	s.running[taskID] = true
	s.canceled[taskID] = true
	s.mu.Unlock()

	s.clearRunState(taskID)

	s.mu.Lock()
	defer s.mu.Unlock()
	assert.False(t, s.running[taskID], "running flag should be cleared")
	assert.False(t, s.canceled[taskID], "canceled flag should be cleared")
}

type recordingClient struct {
	mu           sync.Mutex
	dir          string
	systemPrompt string
	query        string
	mode         string
	output       string
	err          error
}

func (c *recordingClient) Dir(dir string) cli.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dir = dir
	return c
}
func (c *recordingClient) Context(context.Context) cli.Client { return c }
func (c *recordingClient) SkipPermissions() cli.Client        { return c }
func (c *recordingClient) AppendSystemPrompt(p string) cli.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.systemPrompt = p
	return c
}
func (c *recordingClient) Ask(q string) (*cli.Result, error)      { return c.run("ask", q) }
func (c *recordingClient) Continue(q string) (*cli.Result, error) { return c.run("continue", q) }
func (c *recordingClient) run(mode, q string) (*cli.Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mode, c.query = mode, q
	if c.err != nil {
		return &cli.Result{Text: c.output}, c.err
	}
	return &cli.Result{Text: "reply", FullText: "reply"}, nil
}

type recordingProvider struct{ client recordingClient }

func (p *recordingProvider) NewClient() cli.Client    { return &p.client }
func (p *recordingProvider) PreInvoke() error         { return nil }
func (p *recordingProvider) Version() (string, error) { return "1", nil }
func (p *recordingProvider) Name() string             { return "recording" }

func setupRecordingScheduler(t *testing.T) (*Scheduler, *recordingProvider, *invoker.Invoker, *[]string) {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(&model.ScheduledTask{}, &model.TaskRunLog{}))

	provider := &recordingProvider{}
	dataDir := t.TempDir()
	inv := invoker.New(provider, invoker.Options{
		DataDir:  dataDir,
		TaskList: func(chatID int64, threadID int) string { return FormatTaskList(database, time.UTC, chatID, threadID) },
	})
	s, err := New(database, inv, inlineRunner{}, time.UTC)
	require.NoError(t, err)

	var sent []string
	p := pipeline.New(func(_ context.Context, _ pipeline.Dest, text, _ string) error {
		sent = append(sent, text)
		return nil
	}, sendfile.Senders{}, false)
	p.SetDataDir(dataDir)
	s.SetPipeline(p)
	return s, provider, inv, &sent
}

func createRunnableTask(t *testing.T, s *Scheduler, contextMode string) *model.ScheduledTask {
	t.Helper()
	task := &model.ScheduledTask{
		ID: model.GenerateTaskID(), ChatID: 100, ThreadID: 7, Prompt: "remind about water",
		ScheduleType: model.ScheduleInterval, ScheduleValue: "1h",
		ContextMode: contextMode, Status: model.StatusActive, CreatedAt: time.Now(),
	}
	require.NoError(t, s.db.Create(task).Error)
	return task
}

func TestExecuteTask_GroupContinuesChatSessionWithSharedPrompt(t *testing.T) {
	s, provider, inv, sent := setupRecordingScheduler(t)
	task := createRunnableTask(t, s, model.ContextGroup)

	s.executeTask(task.ID)

	c := &provider.client
	assert.Equal(t, "continue", c.mode)
	assert.Equal(t, inv.ChatDir(100, 7), c.dir)
	assert.Contains(t, c.systemPrompt, telegram.Prompt)
	assert.Contains(t, c.systemPrompt, task.ID)
	assert.Contains(t, c.query, "[SCHEDULED TASK")
	assert.Contains(t, c.query, "remind about water")
	assert.Equal(t, []string{"reply"}, *sent)
}

func TestExecuteTask_IsolatedRunsOutsideChatSession(t *testing.T) {
	s, provider, inv, sent := setupRecordingScheduler(t)
	task := createRunnableTask(t, s, model.ContextIsolated)

	s.executeTask(task.ID)

	c := &provider.client
	assert.Equal(t, "ask", c.mode)
	assert.Equal(t, filepath.Join(inv.ChatDir(100, 7), invoker.IsolatedDirName), c.dir)
	assert.Contains(t, c.systemPrompt, telegram.Prompt)
	assert.Equal(t, []string{"reply"}, *sent)
}

func TestExecuteTask_KeepsBoundedRunLogs(t *testing.T) {
	s, _, _, _ := setupRecordingScheduler(t)
	s.Start()
	defer s.Shutdown()
	task := &model.ScheduledTask{
		ID: model.GenerateTaskID(), ChatID: 100, Prompt: "tick", ScheduleType: model.ScheduleInterval, ScheduleValue: "1h",
		ContextMode: model.ContextGroup, Status: model.StatusActive, CreatedAt: time.Now(),
	}
	require.NoError(t, s.CreateTask(task))

	for range keepRunLogs + 3 {
		s.executeTask(task.ID)
	}

	var count int64
	require.NoError(t, s.db.Model(&model.TaskRunLog{}).Where("task_id = ?", task.ID).Count(&count).Error)
	assert.Equal(t, int64(keepRunLogs), count)
}

type countingRunner struct{ calls int }

func (r *countingRunner) Do(_ chatqueue.Key, job chatqueue.Job) error {
	r.calls++
	job.Fn(context.Background())
	return nil
}

func TestExecuteTask_NotifySendsPromptWithoutAgentOrQueue(t *testing.T) {
	s, provider, _, sent := setupRecordingScheduler(t)
	runner := &countingRunner{}
	s.runner = runner
	task := createRunnableTask(t, s, model.ContextNotify)

	s.executeTask(task.ID)

	assert.Empty(t, provider.client.mode)
	assert.Zero(t, runner.calls)
	assert.Equal(t, []string{"remind about water"}, *sent)

	var logs []model.TaskRunLog
	require.NoError(t, s.db.Where("task_id = ?", task.ID).Find(&logs).Error)
	require.Len(t, logs, 1)
	assert.Equal(t, "success", logs[0].Status)
}

func TestExecuteTask_NotifyIgnoresCommandBlocks(t *testing.T) {
	s, _, _, sent := setupRecordingScheduler(t)
	task := createRunnableTask(t, s, model.ContextNotify)
	block := "```nclaw:schedule\n" + `{"action":"create","prompt":"x","type":"interval","value":"1m"}` + "\n```"
	require.NoError(t, s.db.Model(task).Update("prompt", "hi\n"+block).Error)

	s.executeTask(task.ID)

	var count int64
	require.NoError(t, s.db.Model(&model.ScheduledTask{}).Count(&count).Error)
	assert.Equal(t, int64(1), count)
	assert.Equal(t, []string{"hi"}, *sent)
}

func TestExecuteTask_FailureNamesTaskAndShowsOutput(t *testing.T) {
	s, provider, _, sent := setupRecordingScheduler(t)
	provider.client.err = errors.New("exit status 1")
	provider.client.output = "Failed to authenticate"
	task := createRunnableTask(t, s, model.ContextGroup)

	s.executeTask(task.ID)

	require.Len(t, *sent, 1)
	msg := (*sent)[0]
	assert.Contains(t, msg, "Задача по расписанию не выполнилась: remind about water")
	assert.Contains(t, msg, "exit status 1")
	assert.Contains(t, msg, "Failed to authenticate")
	assert.NotContains(t, msg, "Больше она не запустится")
}

func TestExecuteTask_OnceFailureSaysItWillNotRepeat(t *testing.T) {
	s, provider, _, sent := setupRecordingScheduler(t)
	provider.client.err = errors.New("boom")
	task := createRunnableTask(t, s, model.ContextIsolated)
	require.NoError(t, s.db.Model(task).Updates(map[string]any{
		"schedule_type": model.ScheduleOnce, "schedule_value": "2020-01-01T00:00:00",
	}).Error)

	s.executeTask(task.ID)

	require.Len(t, *sent, 1)
	assert.Contains(t, (*sent)[0], "Больше она не запустится.")
	got, err := db.GetTask(s.db, task.ID)
	require.NoError(t, err)
	assert.Equal(t, model.StatusFailed, got.Status)
}

func TestLoadTasks_DisablesUnloadableTaskAndTellsChat(t *testing.T) {
	s, _, _, sent := setupRecordingScheduler(t)
	task := createRunnableTask(t, s, model.ContextGroup)
	require.NoError(t, s.db.Model(task).Update("schedule_type", "weekly").Error)

	s.LoadTasks()

	got, err := db.GetTask(s.db, task.ID)
	require.NoError(t, err)
	assert.Equal(t, model.StatusFailed, got.Status)
	require.Len(t, *sent, 1)
	assert.Contains(t, (*sent)[0], "не удалось поставить в расписание, она отключена: remind about water")
	assert.Contains(t, (*sent)[0], `unknown schedule type "weekly"`)
}

func captureDests(s *Scheduler) *[]pipeline.Dest {
	var dests []pipeline.Dest
	s.SetPipeline(pipeline.New(func(_ context.Context, dest pipeline.Dest, _, _ string) error {
		dests = append(dests, dest)
		return nil
	}, sendfile.Senders{}, false))
	return &dests
}

func TestExecuteTask_NotifyCarriesReminderButtons(t *testing.T) {
	s, _, _, _ := setupRecordingScheduler(t)
	dests := captureDests(s)
	task := createRunnableTask(t, s, model.ContextNotify)

	s.executeTask(task.ID)

	require.Len(t, *dests, 1)
	assert.Equal(t, buttons.Reminder(), (*dests)[0].Buttons)
}

func TestExecuteTask_AgentRunsAndFailuresHaveNoReminderButtons(t *testing.T) {
	s, provider, _, _ := setupRecordingScheduler(t)
	dests := captureDests(s)
	ok := createRunnableTask(t, s, model.ContextGroup)
	s.executeTask(ok.ID)

	provider.client.err = errors.New("boom")
	failing := createRunnableTask(t, s, model.ContextNotify)
	require.NoError(t, s.db.Model(failing).Update("context_mode", model.ContextGroup).Error)
	s.executeTask(failing.ID)

	require.Len(t, *dests, 2)
	assert.Nil(t, (*dests)[0].Buttons)
	assert.Nil(t, (*dests)[1].Buttons)
}

func TestSnooze_SchedulesOneTimeNotify(t *testing.T) {
	s, _, _, _ := setupRecordingScheduler(t)
	s.Start()
	defer s.Shutdown()

	before := time.Now()
	at, err := s.Snooze(100, 7, "⏰ Позвонить стоматологу", 15*time.Minute)
	require.NoError(t, err)

	assert.WithinDuration(t, before.Add(15*time.Minute), at, 2*time.Second)
	tasks, err := s.ChatTasks(100, 7)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "⏰ Позвонить стоматологу", tasks[0].Prompt)
	assert.Equal(t, model.ContextNotify, tasks[0].ContextMode)
	assert.Equal(t, model.ScheduleOnce, tasks[0].ScheduleType)
	assert.Equal(t, at.Format("2006-01-02T15:04:05"), tasks[0].ScheduleValue)
}

func TestChatTasks_OnlyLiveTasksOfThatThread(t *testing.T) {
	s, _, _, _ := setupRecordingScheduler(t)
	active := createRunnableTask(t, s, model.ContextNotify)
	paused := createRunnableTask(t, s, model.ContextNotify)
	require.NoError(t, s.db.Model(paused).Update("status", model.StatusPaused).Error)
	done := createRunnableTask(t, s, model.ContextNotify)
	require.NoError(t, s.db.Model(done).Update("status", model.StatusCompleted).Error)
	other := createRunnableTask(t, s, model.ContextNotify)
	require.NoError(t, s.db.Model(other).Update("thread_id", 8).Error)

	tasks, err := s.ChatTasks(100, 7)

	require.NoError(t, err)
	ids := make([]string, 0, len(tasks))
	for i := range tasks {
		ids = append(ids, tasks[i].ID)
	}
	assert.ElementsMatch(t, []string{active.ID, paused.ID}, ids)
}

func TestChatTasks_NextRunFromTheLiveScheduleInTheSchedulersZone(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(&model.ScheduledTask{}, &model.TaskRunLog{}))
	msk := time.FixedZone("MSK", 3*60*60)
	s, err := New(database, invoker.New(&mockProvider{}, invoker.Options{DataDir: t.TempDir()}), inlineRunner{}, msk)
	require.NoError(t, err)
	s.Start()
	defer s.Shutdown()
	require.NoError(t, s.CreateTask(&model.ScheduledTask{
		ID: model.GenerateTaskID(), ChatID: 100, Prompt: "check the news",
		ScheduleType: model.ScheduleInterval, ScheduleValue: "1h", Status: model.StatusActive, CreatedAt: time.Now(),
	}))

	tasks, err := s.ChatTasks(100, 0)

	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.NotNil(t, tasks[0].NextRun)
	assert.Equal(t, msk, tasks[0].NextRun.Location())
	assert.WithinDuration(t, time.Now().Add(time.Hour), *tasks[0].NextRun, time.Minute)
}

func TestCreateTask_RejectsTinyInterval(t *testing.T) {
	s := setupTestScheduler(t)
	s.Start()
	defer s.Shutdown()

	done := make(chan error, 1)
	go func() {
		done <- s.CreateTask(&model.ScheduledTask{
			ID: "tiny", ChatID: 1, Prompt: "p",
			ScheduleType: model.ScheduleInterval, ScheduleValue: "1ns",
			ContextMode: model.ContextGroup, Status: model.StatusActive, CreatedAt: time.Now(),
		})
	}()

	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("CreateTask with a sub-minute interval hung")
	}

	var count int64
	require.NoError(t, s.db.Model(&model.ScheduledTask{}).Count(&count).Error)
	assert.Zero(t, count, "rejected task must not persist")
}

func TestCreateTask_AllowsMinuteInterval(t *testing.T) {
	s := setupTestScheduler(t)
	s.Start()
	defer s.Shutdown()

	err := s.CreateTask(&model.ScheduledTask{
		ID: "ok", ChatID: 1, Prompt: "p",
		ScheduleType: model.ScheduleInterval, ScheduleValue: "1m",
		ContextMode: model.ContextGroup, Status: model.StatusActive, CreatedAt: time.Now(),
	})
	require.NoError(t, err)
}

func TestCreateTask_RejectsPanicCron(t *testing.T) {
	s := setupTestScheduler(t)
	s.Start()
	defer s.Shutdown()

	err := s.CreateTask(&model.ScheduledTask{
		ID: "badcron", ChatID: 1, Prompt: "p",
		ScheduleType: model.ScheduleCron, ScheduleValue: "TZ=UTC",
		ContextMode: model.ContextGroup, Status: model.StatusActive, CreatedAt: time.Now(),
	})
	require.Error(t, err)

	var count int64
	require.NoError(t, s.db.Model(&model.ScheduledTask{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestLoadTasks_DisablesPanicCron(t *testing.T) {
	s := setupTestScheduler(t)
	require.NoError(t, s.db.Create(&model.ScheduledTask{
		ID: "badcron", ChatID: 1, Prompt: "p",
		ScheduleType: model.ScheduleCron, ScheduleValue: "TZ=UTC",
		ContextMode: model.ContextGroup, Status: model.StatusActive, CreatedAt: time.Now(),
	}).Error)

	require.NotPanics(t, s.LoadTasks)
	require.NotPanics(t, s.Start)
	defer s.Shutdown()

	var got model.ScheduledTask
	require.NoError(t, s.db.First(&got, "id = ?", "badcron").Error)
	assert.Equal(t, model.StatusFailed, got.Status)
}
