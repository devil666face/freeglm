package command

import (
	"context"
	"net/http"

	"freeglm/internal/config"
	"freeglm/internal/server"

	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"
)

type Command struct {
	cmd *cobra.Command
}

func (cmd *Command) server(model *string, listen *string, timeout *int) func(*cobra.Command, []string) error {
	return func(c *cobra.Command, s []string) error {
		_config, err := config.New()
		if err != nil {
			c.Println("config warning:", err)
		}

		_server, err := server.New(
			_config.Keys,
			*model,
			*listen,
			*timeout,
		)
		if err != nil {
			return err
		}

		c.Println("start server:", *listen)
		if err := _server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	}
}

func New() *Command {
	_command := &Command{
		cmd: &cobra.Command{
			Use:   "freeglm",
			Short: "GLM (z.ai) to OpenAI API type",
			Long: `Free proxy from GLM to OpenAI type API.

	- Transform GLM api based requests to OpenAI compatible API
	- Create account via https://chat.z.ai/auth. Use https://reusable.email/ for temp mail
	- Create new API key via https://z.ai/manage-apikey/apikey-list
	- Set ZAI_API_KEY in environment
	- Set FreeGLM in ~/.config/opencode/opencode.jsonc


Main commands:
	freeglm server
		Run freeglm server
`,
			Example: `
freeglm server
freeglm s
freeglm server --model glm-4.7-flash
freeglm server --model glm-4.7
freeglm server --timeout 120
freeglm server --listen 0.0.0.0:5001
ZAI_API_KEY=275dd***************************.**************si freeglm server
`,
			RunE: func(c *cobra.Command, args []string) error {
				return c.Help()
			},
		},
	}

	var (
		model   string
		listen  string
		timeout int
	)

	server := &cobra.Command{
		Use:     "server (alias:s)",
		Aliases: []string{"s"},
		Short:   "Run proxy server",
		Long: `Run proxy server from GLM to OpenAI

By default:
	- uses the "glm-4.7-flash" model
	- timeout is disabled (see the --timeout flag)

Note:
	- set ZAI_API_KEY in environment
	- set many API keys via "," like ZAI_API_KEY=8*****X,c*****a,2*****7
	- if ZAI_API_KEY env not set use config like this in agent (opencode)
	{
	  "$schema": "https://opencode.ai/config.json",
	  "provider": {
	    "FreeGLM": {
	      "npm": "@ai-sdk/openai-compatible",
	      "options": {
	        "baseURL": "http://127.0.0.1:5000/v1",
	        "apiKey": "{env:ZAI_API_KEY}", # this send ZAI_API_KEY with "Authorization: Beared {token}" header
	      },
	      "models": {
	        "glm-4.7-flash": {
	          "attachment": true,
	          "tool_call": true,
	          "reasoning": true,
	        }
	      }
	    },
	  },
	}
`,
		Example: `
freeglm server
Run server with default settings

freeglm server --model glm-4.7-flash
Run server with model "glm-4.7-flash" - reccomended (free to use)

freeglm server --model glm-4.7
Run server with model "glm-4.7" - need "condig plan" on z.ai

freeglm server --timeout 120
Run server with timeout for one request not more then 120 sec.

freeglm server --listen 0.0.0.0:5001
Run server and listen any host on port 5001
`,
		RunE: _command.server(
			&model, &listen, &timeout,
		),
	}
	server.Flags().StringVarP(&model, "model", "m", "glm-4.7-flash", "Model name")
	server.Flags().StringVarP(&listen, "listen", "l", "127.0.0.1:5000", "Server listen")
	server.Flags().IntVarP(&timeout, "timeout", "t", 0, "Seconds of timeout for one request")

	_command.cmd.AddCommand(server)

	return _command
}

func (cmd *Command) Execute(ctx context.Context) error {
	if err := fang.Execute(ctx, cmd.cmd); err != nil {
		return err
	}
	return nil
}
