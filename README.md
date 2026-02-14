# FreeGLM

1. Create account via https://chat.z.ai/auth. Use https://reusable.email/ for temp mail
2. Create new API key via https://z.ai/manage-apikey/apikey-list
3. set ZAI_API_KEY in envs
4. set FreeGLM in ~/.config/opencode/opencode.jsonc

```json
{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "FreeGLM": {
      "npm": "@ai-sdk/openai-compatible",
      "options": {
        "baseURL": "http://127.0.0.1:5000/v1",
        "apiKey": "{env:ZAI_API_KEY}"
      },
      "models": {
        "glm-4.7-flash": {
          "attachment": true,
          "tool_call": true,
          "reasoning": true
        }
      }
    }
  }
}
```

5. Run `freeglm` bin

```bash
>  ./bin/freeglm server
start server: 127.0.0.1:5000
```

6. Test it

```bash
opencode --model FreeGLM/glm-4.7-flash --prompt "Test"
```

---

### More tokens

1. Comment apiKey in config

```json
{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "FreeGLM": {
      "npm": "@ai-sdk/openai-compatible",
      "options": {
        "baseURL": "http://127.0.0.1:5000/v1",
        # "apiKey": "{env:ZAI_API_KEY}" ! - comment this
      },
      "models": {
        "glm-4.7-flash": {
          "attachment": true,
          "tool_call": true,
          "reasoning": true
        }
      }
    }
  }
}
```

2. Set more tokens via ","

```
export ZAI_API_KEY=27*********************************************si,47*********************************************BY,1a*********************************************2T
```

3. Run (tokens will work one by one)

---

### Docker

1.  Set ZAI_API_KEY in env
2.  Run compose

    > ```
    >     environment:
    >         - ZAI_API_KEY=$ZAI_API_KEY
    > ```

    ```
    docker compose up -d
    ```

---

### Build

```bash
go build \
  -tags netgo \
  -ldflags="-extldflags '-static' -w -s -buildid=" \
  -trimpath \
  -gcflags="all=-trimpath=$PWD -dwarf=false -l" \
  -asmflags="all=-trimpath=$PWD" \
  -o freeglm \
  cmd/freeglm/main.go
```
