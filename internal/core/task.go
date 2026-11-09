package core

// TaskState is the persisted state machine of docs/05 §4.
type TaskState string

const (
	StatePending        TaskState = "Pending"
	StateResolving      TaskState = "Resolving"
	StateReady          TaskState = "Ready"
	StateRunning        TaskState = "Running"
	StatePaused         TaskState = "Paused"
	StatePostProcessing TaskState = "PostProcessing"
	StateCompleted      TaskState = "Completed"
	StateFailed         TaskState = "Failed"
	StateCanceled       TaskState = "Canceled"
	StateSeeding        TaskState = "Seeding"
)

// Task is the single record of truth for a download; every source produces one.
type Task struct {
	ID            TaskID      `json:"id"`
	State         TaskState   `json:"state"`
	Input         string      `json:"input"`
	InputKind     InputKind   `json:"inputKind"`
	ResourceID    string      `json:"resourceId,omitempty"`
	Priority      int         `json:"priority"`
	ErrorKind     string      `json:"errorKind,omitempty"`
	ErrorMessage  string      `json:"errorMessage,omitempty"`
	BytesDone     int64       `json:"bytesDone"`
	BytesTotal    int64       `json:"bytesTotal"`
	BytesUploaded int64       `json:"bytesUploaded"`
	CreatedAt     int64       `json:"createdAt"`
	UpdatedAt     int64       `json:"updatedAt"`
	CompletedAt   *int64      `json:"completedAt,omitempty"`
	Options       TaskOptions `json:"options"`
	Resource      *Resource   `json:"resource,omitempty"`
}

// transitions is the complete允许迁移表 (docs/05 §4). P0 additionally allows
// Cancel from every non-running non-terminal state so the CLI/REST contract is
// uniform; that refinement is recorded in docs/05 and CHANGELOG.
var transitions = map[TaskState]map[TaskState]bool{
	StatePending: {
		StateResolving: true, StateCanceled: true,
	},
	StateResolving: {
		StateReady: true, StateFailed: true, StatePaused: true, StateCanceled: true,
	},
	StateReady: {
		StateRunning: true, StatePaused: true, StateCanceled: true,
	},
	StateRunning: {
		StatePaused: true, StatePostProcessing: true, StateFailed: true,
		StateCanceled: true,
	},
	StatePaused: {
		StateRunning: true, StateReady: true, StateCanceled: true, StateFailed: true,
	},
	StatePostProcessing: {
		StateCompleted: true, StateFailed: true, StateCanceled: true, StatePaused: true,
	},
	StateFailed: {
		StateReady: true, StateCanceled: true,
	},
	StateCompleted: {
		StateSeeding: true,
	},
	StateSeeding: {
		StateCompleted: true, StateCanceled: true,
	},
	StateCanceled: {},
}

// CanTransition reports whether from -> to is legal.
func CanTransition(from, to TaskState) error {
	if m, ok := transitions[from]; ok && m[to] {
		return nil
	}
	return Err(E_ILLEGAL_TRANSITION, "illegal state transition %s -> %s", from, to)
}

// IsTerminal reports states from which no worker continues automatically.
func IsTerminal(s TaskState) bool {
	return s == StateCompleted || s == StateCanceled
}

// ActiveStates are the states that must be reset to Paused on crash recovery
// (docs/06 §5 step 2).
var ActiveStates = []TaskState{
	StateResolving, StateRunning, StatePostProcessing, StateSeeding,
}

// ApplyTransition mutates a Task in memory. Persisting it in the same
// transaction as chunk progress is the store/scheduler's responsibility.
func ApplyTransition(t *Task, to TaskState, at int64) error {
	if err := CanTransition(t.State, to); err != nil {
		return err
	}
	t.State = to
	t.UpdatedAt = at
	if to == StateCompleted {
		ts := at
		t.CompletedAt = &ts
	}
	if to != StateFailed {
		t.ErrorKind = ""
		t.ErrorMessage = ""
	}
	return nil
}
