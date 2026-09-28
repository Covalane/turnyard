# Go repository input example

[User guide](README.md) · [中文](../zh/local-go.md)

`local-go` writes editable Session, Environment, and Work JSON inputs from the HEAD of an existing Git repository on this machine. It does not change the source repository. Run these commands from the Turnyard repository root:

```sh
go run ./examples/local-go --repo /path/to/go-repo --out /tmp/turnyard-input \
  --objective 'Add a health check to the service and cover it with tests' \
  --commit-message 'feat: add health check' --deliverable health.go
go build -o bin/turnyard ./cmd/turnyard
bin/turnyard session create --file /tmp/turnyard-input/session.json
```

Use the returned `sessionId` with `task add`, then the returned `taskId` with `task run`, `task wait`, and `task show`. Edit the three generated JSON files as needed and prepare the sandbox image and API key using the [user guide](README.md). Use a new `idempotencyKey` for each independent requirement.
