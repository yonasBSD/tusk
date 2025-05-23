package runner

import (
	"path/filepath"
	"slices"

	"github.com/rliebz/tusk/ui"
)

// Context contains contextual information about a run.
type Context struct {
	// CfgPath is the full path of the configuration file.
	CfgPath string

	// Logger is responsible for logging actions as they occur. It is required to
	// be defined for a Context.
	Logger *ui.Logger

	// Interpreter specifies how a command is meant to be executed.
	Interpreter []string

	taskStack []*Task
}

// Dir is the directory that defines the config file, which is the relative
// directory for all command execution.
func (c Context) Dir() string {
	return filepath.Dir(c.CfgPath)
}

// WithTask adds a sub-task to the task stack.
func (c Context) WithTask(t *Task) Context {
	c.taskStack = append(slices.Clip(c.taskStack), t)
	return c
}

// TaskNames returns the list of task names in the stack, in order. Private
// tasks are filtered out.
func (c Context) TaskNames() []string {
	output := make([]string, 0, len(c.taskStack))
	for _, t := range c.taskStack {
		if !t.Private {
			output = append(output, t.Name)
		}
	}
	return output
}
