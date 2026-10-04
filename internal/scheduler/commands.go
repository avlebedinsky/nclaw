package scheduler

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/nickalie/nclaw/internal/blocks"
	"github.com/nickalie/nclaw/internal/db"
	"github.com/nickalie/nclaw/internal/model"
)

type scheduleCommand struct {
	Action  string `json:"action"`
	Prompt  string `json:"prompt"`
	Type    string `json:"type"`
	Value   string `json:"value"`
	Context string `json:"context"`
	TaskID  string `json:"task_id"`
}

// ExecuteBlocks extracts nclaw:schedule code blocks from text, executes them,
// and returns any status messages (errors). Does not modify the input text.
func (s *Scheduler) ExecuteBlocks(text string, chatID int64, threadID int) string {
	matches := blocks.Schedule.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return ""
	}

	var errs []string

	for _, match := range matches {
		if err := s.executeCommand(match[1], chatID, threadID); err != nil {
			log.Printf("scheduler: command error: %v", err)
			errs = append(errs, err.Error())
		}
	}

	if len(errs) > 0 {
		return "[Schedule error: " + strings.Join(errs, "; ") + "]"
	}
	return ""
}

func (s *Scheduler) executeCommand(jsonStr string, chatID int64, threadID int) error {
	var cmd scheduleCommand
	if err := json.Unmarshal([]byte(jsonStr), &cmd); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}

	log.Printf("scheduler: processing command action=%s task_id=%s", cmd.Action, cmd.TaskID)

	switch cmd.Action {
	case "create":
		return s.createTaskFromCommand(&cmd, chatID, threadID)
	case "pause", "resume", "cancel":
		return s.TaskAction(cmd.Action, cmd.TaskID, chatID, threadID)
	default:
		return fmt.Errorf("unknown action %q", cmd.Action)
	}
}

// TaskAction pauses, resumes or cancels a task of the given chat/thread; a task of another
// chat/thread is reported as not found.
func (s *Scheduler) TaskAction(action, taskID string, chatID int64, threadID int) error {
	if err := s.checkOwner(taskID, chatID, threadID); err != nil {
		return err
	}
	switch action {
	case "pause":
		return s.PauseTask(taskID)
	case "resume":
		return s.ResumeTask(taskID)
	default:
		return s.CancelTask(taskID)
	}
}

func (s *Scheduler) checkOwner(taskID string, chatID int64, threadID int) error {
	task, err := db.GetTask(s.db, taskID)
	if err != nil {
		return fmt.Errorf("task not found: %s: %w", taskID, err)
	}
	if task.ChatID != chatID || task.ThreadID != threadID {
		return fmt.Errorf("task not found: %s", taskID)
	}
	return nil
}

func (s *Scheduler) createTaskFromCommand(cmd *scheduleCommand, chatID int64, threadID int) error {
	if cmd.Prompt == "" || cmd.Type == "" || cmd.Value == "" {
		return fmt.Errorf("create requires prompt, type, and value")
	}

	contextMode, err := parseContext(cmd.Context)
	if err != nil {
		return err
	}

	var nextRun *time.Time
	if cmd.Type == model.ScheduleOnce {
		t, err := time.ParseInLocation("2006-01-02T15:04:05", cmd.Value, s.loc)
		if err != nil {
			return fmt.Errorf("invalid once time %q: %w", cmd.Value, err)
		}
		nextRun = &t
	}

	task := &model.ScheduledTask{
		ID:            model.GenerateTaskID(),
		ChatID:        chatID,
		ThreadID:      threadID,
		Prompt:        cmd.Prompt,
		ScheduleType:  cmd.Type,
		ScheduleValue: cmd.Value,
		ContextMode:   contextMode,
		Status:        model.StatusActive,
		NextRun:       nextRun,
		CreatedAt:     time.Now(),
	}

	return s.CreateTask(task)
}

func parseContext(value string) (string, error) {
	switch value {
	case "":
		return model.ContextGroup, nil
	case model.ContextGroup, model.ContextIsolated, model.ContextNotify:
		return value, nil
	default:
		return "", fmt.Errorf("invalid context %q: want group, isolated or notify", value)
	}
}
