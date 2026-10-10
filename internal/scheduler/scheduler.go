package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/nickalie/nclaw/internal/buttons"
	"github.com/nickalie/nclaw/internal/chatqueue"
	"github.com/nickalie/nclaw/internal/cli"
	"github.com/nickalie/nclaw/internal/db"
	"github.com/nickalie/nclaw/internal/invoker"
	"github.com/nickalie/nclaw/internal/model"
	"github.com/nickalie/nclaw/internal/pipeline"
)

const (
	keepRunLogs           = 20
	finishedTaskRetention = 30 * 24 * time.Hour
	minInterval           = time.Minute
)

// Runner executes a job in its chat's queue and waits for it.
type Runner interface {
	Do(key chatqueue.Key, job chatqueue.Job) error
}

// Scheduler manages scheduled tasks using gocron and SQLite persistence.
type Scheduler struct {
	cron     gocron.Scheduler
	db       *gorm.DB
	invoker  *invoker.Invoker
	runner   Runner
	pipeline *pipeline.Pipeline
	loc      *time.Location
	jobs     map[string]uuid.UUID // taskID -> gocron job UUID
	running  map[string]bool      // tasks currently executing
	canceled map[string]bool      // tasks canceled during execution
	mu       sync.Mutex
}

// New creates a new Scheduler that runs tasks through inv, queued per chat by runner, in loc.
func New(database *gorm.DB, inv *invoker.Invoker, runner Runner, loc *time.Location) (*Scheduler, error) {
	cron, err := gocron.NewScheduler(gocron.WithLocation(loc))
	if err != nil {
		return nil, fmt.Errorf("scheduler: create: %w", err)
	}

	return &Scheduler{
		cron:     cron,
		db:       database,
		invoker:  inv,
		runner:   runner,
		loc:      loc,
		jobs:     make(map[string]uuid.UUID),
		running:  make(map[string]bool),
		canceled: make(map[string]bool),
	}, nil
}

// SetPipeline sets the pipeline for post-Claude response processing.
func (s *Scheduler) SetPipeline(p *pipeline.Pipeline) {
	s.pipeline = p
}

// Start begins the gocron scheduler.
func (s *Scheduler) Start() {
	s.cron.Start()
	log.Println("scheduler: started")
}

// Shutdown stops the gocron scheduler.
func (s *Scheduler) Shutdown() error {
	return s.cron.Shutdown()
}

// LoadTasks removes long-finished tasks, then reads all active tasks from the
// database and registers them with gocron.
func (s *Scheduler) LoadTasks() {
	if n, err := db.DeleteFinishedTasks(s.db, time.Now().Add(-finishedTaskRetention)); err != nil {
		log.Printf("scheduler: delete finished tasks: %v", err)
	} else if n > 0 {
		log.Printf("scheduler: deleted %d finished tasks", n)
	}

	var tasks []model.ScheduledTask
	if err := s.db.Where("status = ?", model.StatusActive).Find(&tasks).Error; err != nil {
		log.Printf("scheduler: load tasks: %v", err)
		return
	}

	var loaded int
	for i := range tasks {
		if err := s.addJob(&tasks[i]); err != nil {
			s.disableUnloadable(&tasks[i], err)
		} else {
			loaded++
		}
	}

	log.Printf("scheduler: loaded %d/%d active tasks", loaded, len(tasks))
}

func (s *Scheduler) disableUnloadable(task *model.ScheduledTask, err error) {
	log.Printf("scheduler: load task %s: %v", task.ID, err)
	if dbErr := db.UpdateTaskStatus(s.db, task.ID, model.StatusFailed); dbErr != nil {
		log.Printf("scheduler: disable task %s: %v", task.ID, dbErr)
	}
	if s.pipeline == nil {
		return
	}
	text := fmt.Sprintf("⚠️ Задачу не удалось поставить в расписание, она отключена: %s\n%v", truncate(task.Prompt, 80), err)
	s.deliver(pipeline.Dest{ChatID: task.ChatID, ThreadID: task.ThreadID}, &cli.Result{Text: text}, nil, "")
}

// CreateTask persists a task and registers it with gocron.
func (s *Scheduler) CreateTask(task *model.ScheduledTask) error {
	if err := db.CreateTask(s.db, task); err != nil {
		return fmt.Errorf("scheduler: create task: %w", err)
	}
	if err := s.addJob(task); err != nil {
		if delErr := db.DeleteTask(s.db, task.ID); delErr != nil {
			log.Printf("scheduler: rollback failed for task %s: %v", task.ID, delErr)
		}
		return fmt.Errorf("scheduler: add job: %w", err)
	}
	log.Printf("scheduler: created task %s (%s: %s) prompt=%q", task.ID, task.ScheduleType, task.ScheduleValue, truncate(task.Prompt, 60))
	return nil
}

// PauseTask pauses a task by removing it from gocron and updating status.
func (s *Scheduler) PauseTask(id string) error {
	task, err := db.GetTask(s.db, id)
	if err != nil {
		return fmt.Errorf("scheduler: get task: %w", err)
	}
	if task.Status != model.StatusActive {
		return fmt.Errorf("scheduler: task %s is %s, not active", id, task.Status)
	}

	if err := db.UpdateTaskStatus(s.db, id, model.StatusPaused); err != nil {
		return err
	}
	s.removeJob(id)
	log.Printf("scheduler: paused task %s", id)
	return nil
}

// ResumeTask resumes a paused task.
func (s *Scheduler) ResumeTask(id string) error {
	task, err := db.GetTask(s.db, id)
	if err != nil {
		return fmt.Errorf("scheduler: get task: %w", err)
	}
	if task.Status != model.StatusPaused {
		return fmt.Errorf("scheduler: task %s is %s, not paused", id, task.Status)
	}

	if err := db.UpdateTaskStatus(s.db, id, model.StatusActive); err != nil {
		return err
	}
	task.Status = model.StatusActive

	if err := s.addJob(task); err != nil {
		if rbErr := db.UpdateTaskStatus(s.db, id, model.StatusPaused); rbErr != nil {
			log.Printf("scheduler: rollback failed for task %s: %v", id, rbErr)
		}
		return fmt.Errorf("scheduler: resume add job: %w", err)
	}
	log.Printf("scheduler: resumed task %s", id)
	return nil
}

// CancelTask deletes a task entirely.
func (s *Scheduler) CancelTask(id string) error {
	s.mu.Lock()
	if s.running[id] {
		s.canceled[id] = true
	}
	s.mu.Unlock()

	if err := db.DeleteTask(s.db, id); err != nil {
		s.mu.Lock()
		delete(s.canceled, id)
		s.mu.Unlock()
		return fmt.Errorf("scheduler: delete task: %w", err)
	}
	s.removeJob(id)
	log.Printf("scheduler: canceled task %s", id)
	return nil
}

// addJob creates a gocron job for the given task.
func (s *Scheduler) addJob(task *model.ScheduledTask) error {
	def, err := s.jobDefinition(task)
	if err != nil {
		return err
	}

	taskID := task.ID
	job, err := s.cron.NewJob(
		def,
		gocron.NewTask(s.executeTask, taskID),
		gocron.WithName(taskID),
		gocron.WithSingletonMode(gocron.LimitModeReschedule),
	)
	if err != nil {
		return fmt.Errorf("new job: %w", err)
	}

	s.mu.Lock()
	s.jobs[taskID] = job.ID()
	s.mu.Unlock()

	return nil
}

// removeJob removes a gocron job for the given task ID.
func (s *Scheduler) removeJob(taskID string) {
	s.mu.Lock()
	jobID, ok := s.jobs[taskID]
	if ok {
		delete(s.jobs, taskID)
	}
	s.mu.Unlock()

	if ok {
		s.cron.RemoveJob(jobID) //nolint:errcheck // job already removed from map; cron cleanup is best-effort
	}
}

func (s *Scheduler) jobDefinition(task *model.ScheduledTask) (gocron.JobDefinition, error) {
	switch task.ScheduleType {
	case model.ScheduleCron:
		return gocron.CronJob(task.ScheduleValue, false), nil
	case model.ScheduleInterval:
		d, err := time.ParseDuration(task.ScheduleValue)
		if err != nil {
			return nil, fmt.Errorf("parse interval %q: %w", task.ScheduleValue, err)
		}
		if d < minInterval {
			return nil, fmt.Errorf("interval %s is too short, minimum is %s", d, minInterval)
		}
		return gocron.DurationJob(d), nil
	case model.ScheduleOnce:
		t, err := time.ParseInLocation("2006-01-02T15:04:05", task.ScheduleValue, s.loc)
		if err != nil {
			return nil, fmt.Errorf("parse once time %q: %w", task.ScheduleValue, err)
		}
		if t.Before(time.Now()) {
			log.Printf("scheduler: once task %s scheduled for %s is in the past, running immediately", task.ID, task.ScheduleValue)
			return gocron.OneTimeJob(gocron.OneTimeJobStartImmediately()), nil
		}
		return gocron.OneTimeJob(gocron.OneTimeJobStartDateTime(t)), nil
	default:
		return nil, fmt.Errorf("unknown schedule type %q", task.ScheduleType)
	}
}

func (s *Scheduler) executeTask(taskID string) {
	s.mu.Lock()
	s.running[taskID] = true
	s.mu.Unlock()
	defer s.clearRunState(taskID)

	task, err := s.activeTask(taskID)
	if err != nil {
		log.Printf("scheduler: skipping task %s: %v", taskID, err)
		return
	}

	if task.ContextMode == model.ContextNotify {
		s.runTask(context.Background(), taskID)
		return
	}

	key := chatqueue.Key{ChatID: task.ChatID, ThreadID: task.ThreadID}
	job := chatqueue.Job{Kind: chatqueue.KindScheduled, Label: truncate(task.Prompt, 40), Fn: func(ctx context.Context) {
		s.runTask(ctx, taskID)
	}}
	if err := s.runner.Do(key, job); err != nil {
		log.Printf("scheduler: task %s not run: %v", taskID, err)
	}
}

func (s *Scheduler) activeTask(taskID string) (*model.ScheduledTask, error) {
	task, err := db.GetTask(s.db, taskID)
	if err != nil {
		return nil, err
	}
	if task.Status != model.StatusActive {
		return nil, fmt.Errorf("status is %s", task.Status)
	}
	return task, nil
}

func (s *Scheduler) runTask(ctx context.Context, taskID string) {
	task, err := s.activeTask(taskID)
	if err != nil {
		log.Printf("scheduler: skipping task %s: %v", taskID, err)
		return
	}

	log.Printf("scheduler: executing task %s (%s: %s) prompt=%q",
		taskID, task.ScheduleType, task.ScheduleValue, truncate(task.Prompt, 60))

	start := time.Now()
	out := s.execute(ctx, task)
	result, runErr := out.Result, out.Err
	duration := time.Since(start)

	if errors.Is(runErr, chatqueue.ErrShuttingDown) {
		log.Printf("scheduler: task %s interrupted by shutdown, will run again after restart", taskID)
		return
	}
	if runErr != nil {
		log.Printf("scheduler: task %s failed after %s: %v", taskID, duration, runErr)
	} else {
		log.Printf("scheduler: task %s completed in %s, reply_len=%d", taskID, duration, len(result.Text))
	}

	s.finalizeAndSend(task, out, duration)
}

// finalizeAndSend records run results and sends the reply, unless the task was canceled.
func (s *Scheduler) finalizeAndSend(task *model.ScheduledTask, out invoker.Outcome, duration time.Duration) {
	result, runErr := out.Result, out.Err
	// Atomically verify task still exists and record results.
	err := s.recordResults(task, result.Text, runErr, duration)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		log.Printf("scheduler: task %s was deleted during execution, skipping send", task.ID)
		return
	}
	if err != nil {
		log.Printf("scheduler: task %s record results failed: %v", task.ID, err)
	}

	if task.ScheduleType == model.ScheduleOnce {
		s.removeJob(task.ID)
	}

	// Atomically check canceled and exit "running" state so CancelTask
	// can no longer flag this execution after we decide to send.
	s.mu.Lock()
	wasCanceled := s.canceled[task.ID]
	delete(s.canceled, task.ID)
	delete(s.running, task.ID)
	s.mu.Unlock()

	if wasCanceled {
		log.Printf("scheduler: task %s was canceled after execution, skipping send", task.ID)
		return
	}

	s.sendResult(task, result, runErr, out.Dir)
}

// clearRunState removes the task from both the running and canceled sets.
func (s *Scheduler) clearRunState(taskID string) {
	s.mu.Lock()
	delete(s.running, taskID)
	delete(s.canceled, taskID)
	s.mu.Unlock()
}

func (s *Scheduler) execute(ctx context.Context, task *model.ScheduledTask) invoker.Outcome {
	if task.ContextMode == model.ContextNotify {
		return invoker.Outcome{Result: &cli.Result{Text: task.Prompt}}
	}
	return s.invokeCLI(ctx, task)
}

func (s *Scheduler) invokeCLI(ctx context.Context, task *model.ScheduledTask) invoker.Outcome {
	mode := invoker.Continue
	if task.ContextMode == model.ContextIsolated {
		mode = invoker.Isolated
	}

	log.Printf("scheduler: invoking %s for task %s context=%s", s.invoker.ProviderName(), task.ID, task.ContextMode)
	return s.invoker.Run(ctx, invoker.Request{
		ChatID:   task.ChatID,
		ThreadID: task.ThreadID,
		Prompt:   "[SCHEDULED TASK - Running automatically, not in response to a user message]\n\n" + task.Prompt,
		Mode:     mode,
	})
}

// recordResults atomically verifies the task still exists and records the run outcome.
// Returns gorm.ErrRecordNotFound if the task was deleted (e.g. canceled during execution).
func (s *Scheduler) recordResults(task *model.ScheduledTask, reply string, runErr error, duration time.Duration) error {
	nextRun := s.resolveNextRun(task)
	return s.db.Transaction(func(tx *gorm.DB) error {
		if _, err := db.GetTask(tx, task.ID); err != nil {
			return err
		}
		if err := s.logRunTx(tx, task.ID, reply, runErr, duration); err != nil {
			return err
		}
		return s.updateAfterRunTx(tx, task, nextRun, reply, runErr)
	})
}

func (s *Scheduler) logRunTx(tx *gorm.DB, taskID, reply string, runErr error, duration time.Duration) error {
	runLog := &model.TaskRunLog{
		TaskID:     taskID,
		RunAt:      time.Now(),
		DurationMs: duration.Milliseconds(),
		Status:     "success",
	}
	if runErr != nil {
		errStr := runErr.Error()
		runLog.Status = "error"
		runLog.Error = &errStr
	} else {
		runLog.Result = &reply
	}
	if err := db.LogRun(tx, runLog); err != nil {
		return err
	}
	return db.PruneRunLogs(tx, taskID, keepRunLogs)
}

func (s *Scheduler) updateAfterRunTx(
	tx *gorm.DB, task *model.ScheduledTask, nextRun *time.Time, reply string, runErr error,
) error {
	resultSummary := truncate(reply, 200)
	if runErr != nil {
		resultSummary = "Error: " + runErr.Error()
	}
	if err := db.UpdateTaskAfterRun(tx, task.ID, nextRun, resultSummary); err != nil {
		return err
	}
	if runErr != nil && task.ScheduleType == model.ScheduleOnce {
		return db.UpdateTaskStatus(tx, task.ID, model.StatusFailed)
	}
	return nil
}

func (s *Scheduler) resolveNextRun(task *model.ScheduledTask) *time.Time {
	if task.ScheduleType == model.ScheduleOnce {
		return nil
	}

	s.mu.Lock()
	jobID, ok := s.jobs[task.ID]
	s.mu.Unlock()

	if !ok {
		return nil
	}
	return s.getNextRun(jobID)
}

func (s *Scheduler) sendResult(task *model.ScheduledTask, result *cli.Result, runErr error, dir string) {
	if errors.Is(runErr, chatqueue.ErrStopped) {
		log.Printf("scheduler: task %s stopped by user, skipping send", task.ID)
		return
	}
	if s.pipeline == nil {
		log.Printf("scheduler: pipeline not ready, dropping result for task %s", task.ID)
		return
	}

	dest := pipeline.Dest{ChatID: task.ChatID, ThreadID: task.ThreadID}
	switch {
	case runErr != nil:
		result = failureResult(task, result, runErr)
	case task.ContextMode == model.ContextNotify:
		dest.Buttons = buttons.Reminder()
	}
	s.deliver(dest, result, runErr, dir)
}

func (s *Scheduler) deliver(dest pipeline.Dest, result *cli.Result, runErr error, dir string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	s.pipeline.Process(ctx, result, runErr, dest, dir, false)
}

// Snooze schedules text as a one-time reminder in a chat/thread after the given delay
// and returns when it will fire.
func (s *Scheduler) Snooze(chatID int64, threadID int, text string, after time.Duration) (time.Time, error) {
	at := time.Now().In(s.loc).Add(after).Truncate(time.Second)
	return at, s.CreateTask(&model.ScheduledTask{
		ID:            model.GenerateTaskID(),
		ChatID:        chatID,
		ThreadID:      threadID,
		Prompt:        text,
		ScheduleType:  model.ScheduleOnce,
		ScheduleValue: at.Format("2006-01-02T15:04:05"),
		ContextMode:   model.ContextNotify,
		Status:        model.StatusActive,
		NextRun:       &at,
		CreatedAt:     time.Now(),
	})
}

// ChatTasks returns the active and paused tasks of a chat/thread, with the next run taken
// from the live schedule where there is one, in the scheduler's time zone.
func (s *Scheduler) ChatTasks(chatID int64, threadID int) ([]model.ScheduledTask, error) {
	tasks, err := liveTasks(s.db, chatID, threadID)
	if err != nil {
		return nil, err
	}
	for i := range tasks {
		next := s.resolveNextRun(&tasks[i])
		if next == nil {
			next = tasks[i].NextRun
		}
		if next != nil {
			at := next.In(s.loc)
			tasks[i].NextRun = &at
		}
	}
	return tasks, nil
}

func liveTasks(database *gorm.DB, chatID int64, threadID int) ([]model.ScheduledTask, error) {
	tasks, err := db.ListTasksByChat(database, chatID, threadID)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(tasks, func(t model.ScheduledTask) bool {
		return t.Status != model.StatusActive && t.Status != model.StatusPaused
	}), nil
}

func failureResult(task *model.ScheduledTask, result *cli.Result, runErr error) *cli.Result {
	text := fmt.Sprintf("⚠️ Задача по расписанию не выполнилась: %s\n%v", truncate(task.Prompt, 80), runErr)
	if task.ScheduleType == model.ScheduleOnce {
		text += "\nБольше она не запустится."
	}
	if result != nil && strings.TrimSpace(result.Text) != "" {
		text += "\n\n" + strings.TrimSpace(result.Text)
	}
	return &cli.Result{Text: text}
}

func (s *Scheduler) getNextRun(jobID uuid.UUID) *time.Time {
	for _, j := range s.cron.Jobs() {
		if j.ID() == jobID {
			t, err := j.NextRun()
			if err == nil {
				return &t
			}
		}
	}
	return nil
}

// FormatTaskList returns a system prompt section listing the tasks of a chat/thread,
// with next run times shown in loc.
func FormatTaskList(database *gorm.DB, loc *time.Location, chatID int64, threadID int) string {
	tasks, err := liveTasks(database, chatID, threadID)
	if err != nil {
		log.Printf("scheduler: list tasks: %v", err)
		return "Current scheduled tasks: none"
	}
	if len(tasks) == 0 {
		return "Current scheduled tasks: none"
	}

	var b strings.Builder
	b.WriteString("Current scheduled tasks:\n")
	for i := range tasks {
		next := "N/A"
		if tasks[i].NextRun != nil {
			next = tasks[i].NextRun.In(loc).Format("2006-01-02 15:04:05")
		}
		prompt := truncate(tasks[i].Prompt, 80)
		fmt.Fprintf(&b, "- [%s] %s (%s: %s) context=%s status=%s next=%s\n",
			tasks[i].ID, prompt, tasks[i].ScheduleType, tasks[i].ScheduleValue, tasks[i].ContextMode, tasks[i].Status, next)
	}
	return b.String()
}

func truncate(str string, maxLen int) string {
	runes := []rune(str)
	if len(runes) <= maxLen {
		return str
	}
	return string(runes[:maxLen])
}
