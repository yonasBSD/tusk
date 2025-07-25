package runner

import (
	"errors"
	"fmt"
	"os"

	yaml "gopkg.in/yaml.v2"

	"github.com/rliebz/tusk/marshal"
)

// executionState indicates whether a task is "running" or "finally".
type executionState int

const (
	stateRunning executionState = iota
	stateFinally executionState = iota
)

// Task is a single task to be run by CLI.
type Task struct {
	Args    Args    `yaml:"args,omitempty"`
	Options Options `yaml:"options,omitempty"`

	RunList     marshal.Slice[*Run] `yaml:"run"`
	Finally     marshal.Slice[*Run] `yaml:"finally,omitempty"`
	Usage       string              `yaml:"usage,omitempty"`
	Description string              `yaml:"description,omitempty"`
	Private     bool                `yaml:"private"`
	Quiet       bool                `yaml:"quiet"`

	Source marshal.Slice[string] `yaml:"source"`
	Target marshal.Slice[string] `yaml:"target"`

	// Computed members not specified in yaml file
	Name string            `yaml:"-"`
	Vars map[string]string `yaml:"-"`
}

// UnmarshalYAML unmarshals and assigns names to options.
func (t *Task) UnmarshalYAML(unmarshal func(any) error) error {
	var includeTarget Task
	includeCandidate := marshal.UnmarshalCandidate{
		Unmarshal: func() error {
			var def struct {
				Include string            `yaml:"include"`
				Else    map[string]string `yaml:",inline"`
			}

			if err := unmarshal(&def); err != nil {
				return err
			}

			if def.Include == "" {
				// A yaml.TypeError signals to keep trying other candidates.
				return &yaml.TypeError{Errors: []string{`"include" not specified`}}
			}

			if len(def.Else) != 0 {
				return errors.New(`tasks using "include" may not specify other fields`)
			}

			f, err := os.Open(def.Include)
			if err != nil {
				return fmt.Errorf("opening included file: %w", err)
			}
			defer f.Close() //nolint:errcheck

			decoder := yaml.NewDecoder(f)
			decoder.SetStrict(true)

			if err := decoder.Decode(&includeTarget); err != nil {
				//nolint:errorlint // opaque the error to prevent unmarshal retries
				return fmt.Errorf("decoding included file %q: %v", def.Include, err)
			}

			return nil
		},
		Assign: func() { *t = includeTarget },
	}

	var taskTarget Task
	taskCandidate := marshal.UnmarshalCandidate{
		Unmarshal: func() error {
			type taskType Task // Use new type to avoid recursion
			return unmarshal((*taskType)(&taskTarget))
		},
		Validate: taskTarget.isValid,
		Assign:   func() { *t = taskTarget },
	}

	return marshal.UnmarshalOneOf(includeCandidate, taskCandidate)
}

// isValid checks whether a given task definition is valid.
func (t *Task) isValid() error {
	if len(t.Source) > 0 && len(t.Target) == 0 {
		return errors.New("task source cannot be defined without target")
	}

	if len(t.Target) > 0 && len(t.Source) == 0 {
		return errors.New("task target cannot be defined without source")
	}

	for _, o := range t.Options {
		for _, a := range t.Args {
			if o.Name == a.Name {
				return fmt.Errorf(
					"argument and option %q must have unique names within a task", o.Name,
				)
			}
		}
	}

	return nil
}

// AllRunItems returns all run items referenced, including `run` and `finally`.
func (t *Task) AllRunItems() marshal.Slice[*Run] {
	return append(t.RunList, t.Finally...)
}

// Dependencies returns a list of options that are required explicitly.
// This does not include interpolations.
func (t *Task) Dependencies() []string {
	options := make([]string, 0, len(t.Options)+len(t.AllRunItems()))

	for _, opt := range t.Options {
		options = append(options, opt.Dependencies()...)
	}
	for _, run := range t.AllRunItems() {
		options = append(options, run.When.Dependencies()...)
	}

	return options
}

// Execute runs the Run scripts in the task.
func (t *Task) Execute(ctx Context) (err error) {
	ctx = ctx.WithTask(t)

	cachePath, err := t.taskInputCachePath(ctx)
	if err != nil {
		return err
	}

	isUpToDate, err := t.isUpToDate(ctx, cachePath)
	if err != nil {
		return fmt.Errorf("checking cache: %w", err)
	}
	if isUpToDate {
		ctx.Logger.PrintTaskSkipped(t.Name, "all targets up to date")
		return nil
	}

	ctx.Logger.PrintTask(t.Name)

	defer ctx.Logger.PrintTaskCompleted(t.Name)
	defer t.runFinally(ctx, &err)

	for _, r := range t.RunList {
		if err := t.run(ctx, r, stateRunning); err != nil {
			return err
		}
	}

	if err := t.cache(ctx, cachePath); err != nil {
		return fmt.Errorf("caching task: %w", err)
	}

	return nil
}

func (t *Task) runFinally(ctx Context, err *error) {
	if len(t.Finally) == 0 {
		return
	}

	ctx.Logger.PrintTaskFinally(t.Name)

	for _, r := range t.Finally {
		if rerr := t.run(ctx, r, stateFinally); rerr != nil {
			// Do not overwrite existing errors
			if *err == nil {
				*err = rerr
			}
			return
		}
	}
}

// run executes a Run struct.
func (t *Task) run(ctx Context, r *Run, s executionState) error {
	if ok, err := r.shouldRun(ctx, t.Vars); !ok || err != nil {
		return err
	}

	runFuncs := []func() error{
		func() error { return t.runCommands(ctx, r, s) },
		func() error { return t.runSubTasks(ctx, r) },
		func() error { return t.runEnvironment(ctx, r) },
	}

	for _, f := range runFuncs {
		if err := f(); err != nil {
			return err
		}
	}

	return nil
}

// shouldBeQuiet checks if the command or any of the tasks in the stack are quiet.
func shouldBeQuiet(cmd *Command, ctx Context) bool {
	if cmd.Quiet {
		return true
	}
	for _, t := range ctx.taskStack {
		if t.Quiet {
			return true
		}
	}
	return false
}

func (t *Task) runCommands(ctx Context, r *Run, s executionState) error {
	for _, command := range r.Command {
		if !shouldBeQuiet(command, ctx) {
			switch s {
			case stateFinally:
				ctx.Logger.PrintCommandWithParenthetical(command.Print, "finally", ctx.TaskNames()...)
			default:
				ctx.Logger.PrintCommand(command.Print, ctx.TaskNames()...)
			}
		}

		if err := command.exec(ctx); err != nil {
			ctx.Logger.PrintCommandError(err)
			return err
		}
	}

	return nil
}

func (t *Task) runSubTasks(ctx Context, r *Run) error {
	for i := range r.Tasks {
		if err := r.Tasks[i].Execute(ctx); err != nil {
			return err
		}
	}

	return nil
}

func (t *Task) runEnvironment(ctx Context, r *Run) error {
	ctx.Logger.PrintEnvironment(r.SetEnvironment)
	for key, value := range r.SetEnvironment {
		if value == nil {
			if err := os.Unsetenv(key); err != nil {
				return err
			}

			continue
		}

		if err := os.Setenv(key, *value); err != nil {
			return err
		}
	}

	return nil
}
