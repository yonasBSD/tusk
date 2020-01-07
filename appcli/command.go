package appcli

import (
	"fmt"
	"strings"

	"github.com/urfave/cli"

	"github.com/rliebz/tusk/config"
)

type commandCreator func(app *cli.App, t *config.Task) (*cli.Command, error)

func createExecuteCommand(_ *cli.App, t *config.Task) (*cli.Command, error) {
	return createCommand(t, func(c *cli.Context) error {
		if len(t.Args) != len(c.Args()) {
			return fmt.Errorf(
				"task %q requires exactly %d args, got %d",
				t.Name, len(t.Args), len(c.Args()),
			)
		}
		return t.Execute(config.RunContext{})
	}), nil
}

func createMetadataBuildCommand(app *cli.App, t *config.Task) (*cli.Command, error) {
	argsPassed, flagsPassed, err := getPassedValues(app)
	if err != nil {
		return nil, err
	}

	return createCommand(t, func(c *cli.Context) error {
		app.Metadata["command"] = &c.Command
		for _, value := range c.Args() {
			argsPassed = append(argsPassed, value)
		}
		app.Metadata["argsPassed"] = argsPassed
		for _, flagName := range c.FlagNames() {
			if c.IsSet(flagName) {
				flagsPassed[flagName] = c.String(flagName)
			}
		}
		return nil
	}), nil
}

// createCommand creates a cli.Command from a config.config.
func createCommand(t *config.Task, actionFunc func(*cli.Context) error) *cli.Command {
	command := &cli.Command{
		Name:        t.Name,
		Usage:       strings.TrimSpace(t.Usage),
		Description: strings.TrimSpace(t.Description),
		Action:      actionFunc,
	}

	for _, arg := range t.Args {
		command.ArgsUsage += fmt.Sprintf("<%s> ", arg.Name)
	}

	command.CustomHelpTemplate = createCommandHelp(t)

	return command
}
