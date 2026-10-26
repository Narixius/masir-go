package update

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

var (
	ErrBusy       = errors.New("an update is already running; pause or stop it first")
	ErrNotRunning = errors.New("no update is running")
	ErrNotPaused  = errors.New("the update isn't paused")
	ErrCritical   = errors.New("the update is installing and can't be interrupted now")

	errPaused  = errors.New("paused")
	errStopped = errors.New("stopped")
)

// Task does a whole job. It must be safe to run again after a pause: downloads resume
// from the partial files in the work directory. It reports through the Reporter.
type Task func(ctx context.Context, r *Reporter) error

// Runner runs one target's job at a time and tells watchers about every change.
type Runner struct {
	target  pb.UpdateTarget
	workDir string // partial downloads; deleted when a job is stopped

	mu       sync.Mutex
	job      *pb.UpdateJob
	task     Task
	cancel   context.CancelCauseFunc
	done     chan struct{} // closed when the task's goroutine returns
	watchers map[chan struct{}]struct{}
}

func NewRunner(target pb.UpdateTarget, workDir string) *Runner {
	return &Runner{
		target:   target,
		workDir:  workDir,
		job:      &pb.UpdateJob{Target: target},
		watchers: map[chan struct{}]struct{}{},
	}
}

// Snapshot is the job as it is now.
func (r *Runner) Snapshot() *pb.UpdateJob {
	r.mu.Lock()
	defer r.mu.Unlock()
	return proto.Clone(r.job).(*pb.UpdateJob)
}

// Active reports whether a job is running or paused.
func (r *Runner) Active() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.task != nil
}

// Start runs task as a new job.
func (r *Runner) Start(task Task) (*pb.UpdateJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.task != nil {
		return nil, ErrBusy
	}
	r.task = task
	r.job = &pb.UpdateJob{Target: r.target, State: pb.UpdateState_UPDATE_STATE_CHECKING, StartedAtMs: time.Now().UnixMilli()}
	r.runLocked()
	return proto.Clone(r.job).(*pb.UpdateJob), nil
}

// Pause stops the download and keeps what arrived. Only a download can pause.
func (r *Runner) Pause() (*pb.UpdateJob, error) {
	r.mu.Lock()
	switch {
	case r.task == nil:
		r.mu.Unlock()
		return nil, ErrNotRunning
	case r.job.State == pb.UpdateState_UPDATE_STATE_PAUSED:
		defer r.mu.Unlock()
		return proto.Clone(r.job).(*pb.UpdateJob), nil
	case r.job.State != pb.UpdateState_UPDATE_STATE_DOWNLOADING && r.job.State != pb.UpdateState_UPDATE_STATE_CHECKING:
		r.mu.Unlock()
		return nil, ErrCritical
	}
	r.cancel(errPaused)
	done := r.done
	r.mu.Unlock()
	<-done
	return r.Snapshot(), nil
}

// Resume continues a paused job.
func (r *Runner) Resume() (*pb.UpdateJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.task == nil || r.job.State != pb.UpdateState_UPDATE_STATE_PAUSED {
		return nil, ErrNotPaused
	}
	r.job.Error = ""
	r.setLocked(pb.UpdateState_UPDATE_STATE_DOWNLOADING)
	r.runLocked()
	return proto.Clone(r.job).(*pb.UpdateJob), nil
}

// Stop ends a running or paused job and deletes its partial downloads.
func (r *Runner) Stop() (*pb.UpdateJob, error) {
	r.mu.Lock()
	switch {
	case r.task == nil:
		r.mu.Unlock()
		return nil, ErrNotRunning
	case r.job.State == pb.UpdateState_UPDATE_STATE_INSTALLING || r.job.State == pb.UpdateState_UPDATE_STATE_RESTARTING:
		r.mu.Unlock()
		return nil, ErrCritical
	case r.job.State == pb.UpdateState_UPDATE_STATE_PAUSED: // nothing running
		r.finishLocked(pb.UpdateState_UPDATE_STATE_STOPPED, "")
		r.mu.Unlock()
		os.RemoveAll(r.workDir)
		return r.Snapshot(), nil
	}
	r.cancel(errStopped)
	done := r.done
	r.mu.Unlock()
	<-done
	return r.Snapshot(), nil
}

// Watch returns a channel that receives after every change, and a function to stop.
func (r *Runner) Watch() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	r.mu.Lock()
	r.watchers[ch] = struct{}{}
	r.mu.Unlock()
	return ch, func() {
		r.mu.Lock()
		delete(r.watchers, ch)
		r.mu.Unlock()
	}
}

// Finished reports whether a job state is final.
func Finished(s pb.UpdateState) bool {
	switch s {
	case pb.UpdateState_UPDATE_STATE_DONE, pb.UpdateState_UPDATE_STATE_UP_TO_DATE,
		pb.UpdateState_UPDATE_STATE_FAILED, pb.UpdateState_UPDATE_STATE_STOPPED,
		pb.UpdateState_UPDATE_STATE_RESTARTING, pb.UpdateState_UPDATE_STATE_BLOCKED:
		return true
	}
	return false
}

// runLocked starts the task's goroutine. Expects r.mu held.
func (r *Runner) runLocked() {
	ctx, cancel := context.WithCancelCause(context.Background())
	r.cancel = cancel
	r.done = make(chan struct{})
	task, done := r.task, r.done
	rep := &Reporter{r: r}
	go func() {
		defer close(done)
		err := task(ctx, rep)
		r.mu.Lock()
		defer r.mu.Unlock()
		switch cause := context.Cause(ctx); {
		case errors.Is(cause, errPaused):
			r.setLocked(pb.UpdateState_UPDATE_STATE_PAUSED)
		case errors.Is(cause, errStopped):
			r.finishLocked(pb.UpdateState_UPDATE_STATE_STOPPED, "")
			os.RemoveAll(r.workDir)
		case err != nil:
			r.finishLocked(pb.UpdateState_UPDATE_STATE_FAILED, err.Error())
		case !Finished(r.job.State):
			r.finishLocked(pb.UpdateState_UPDATE_STATE_DONE, "")
		default: // the task set its own final state (up to date, restarting)
			r.finishLocked(r.job.State, "")
		}
		cancel(nil)
	}()
}

// finishLocked ends the job in state. Expects r.mu held.
func (r *Runner) finishLocked(state pb.UpdateState, msg string) {
	r.task = nil
	r.job.Error = msg
	r.job.FinishedAtMs = time.Now().UnixMilli()
	r.setLocked(state)
}

func (r *Runner) setLocked(state pb.UpdateState) {
	r.job.State = state
	r.notifyLocked()
}

func (r *Runner) notifyLocked() {
	for ch := range r.watchers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Reporter is how a task describes its progress.
type Reporter struct{ r *Runner }

func (p *Reporter) update(fn func(j *pb.UpdateJob)) {
	p.r.mu.Lock()
	defer p.r.mu.Unlock()
	fn(p.r.job)
	p.r.notifyLocked()
}

// Snapshot is the job as it is now, for analytics at the end of a task.
func (p *Reporter) Snapshot() *pb.UpdateJob { return p.r.Snapshot() }

func (p *Reporter) State(s pb.UpdateState) { p.update(func(j *pb.UpdateJob) { j.State = s }) }
func (p *Reporter) Version(v string)       { p.update(func(j *pb.UpdateJob) { j.Version = v }) }
func (p *Reporter) File(name string)       { p.update(func(j *pb.UpdateJob) { j.File = name }) }
func (p *Reporter) StagedDir(d string)     { p.update(func(j *pb.UpdateJob) { j.StagedDir = d }) }

// Blocked ends the job without installing: the new data can't build these enabled rules.
func (p *Reporter) Blocked(problems []*pb.RuleProblem) {
	p.update(func(j *pb.UpdateJob) {
		j.BlockedRules = problems
		j.State = pb.UpdateState_UPDATE_STATE_BLOCKED
	})
}

// Bytes sets the progress: done so far (including partial files from before a pause)
// and the total.
func (p *Reporter) Bytes(done, total int64) {
	p.update(func(j *pb.UpdateJob) { j.BytesDone, j.BytesTotal = done, total })
}

// Add counts n more bytes downloaded (negative when a download starts over).
func (p *Reporter) Add(n int64) {
	p.update(func(j *pb.UpdateJob) { j.BytesDone = max(j.BytesDone+n, 0) })
}
