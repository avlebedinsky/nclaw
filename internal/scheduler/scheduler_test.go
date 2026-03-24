package scheduler

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nickalie/nclaw/internal/model"
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
