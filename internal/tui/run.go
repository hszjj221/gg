package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
)

func Run(ctx context.Context, config Config) error {
	model := NewModel(config)
	options := []tea.ProgramOption{tea.WithAltScreen()}
	if config.Input != nil {
		options = append(options, tea.WithInput(config.Input))
	}
	if config.Output != nil {
		options = append(options, tea.WithOutput(config.Output))
	}
	program := tea.NewProgram(model, options...)
	errCh := make(chan error, 1)
	go func() {
		_, err := program.Run()
		errCh <- err
	}()
	select {
	case <-ctx.Done():
		program.Quit()
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}
