package freeglm

import (
	"context"

	"freeglm/internal/command"
)

type FreeGLM struct {
	cmd *command.Command
}

func New() (*FreeGLM, error) {
	_cmd := command.New()
	return &FreeGLM{
		cmd: _cmd,
	}, nil
}

func (f *FreeGLM) Start() error {
	return f.cmd.Execute(context.Background())
}
