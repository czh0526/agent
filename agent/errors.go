package agent

import (
	"errors"
	"fmt"
	"reflect"
)

var (
	ErrAgentRunning = errors.New("agent already running")
)

type DuplicateServiceError struct {
	Kind reflect.Type
}

func (e *DuplicateServiceError) Error() string {
	return fmt.Sprintf("duplicate service: %v", e.Kind)
}
