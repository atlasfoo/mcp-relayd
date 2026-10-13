package process

import "context"

// processTree owns a launched process and its descendants. Done reports the
// root's exit once; Stop also terminates descendants and reaps the root.
type processTree interface {
	PID() int
	Done() <-chan error
	Stop(context.Context) error
}
