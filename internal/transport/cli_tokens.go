package transport

// CLI tokens form the user-facing command vocabulary. Keep them distinct from
// RPC actions: several CLI verbs map to the same supervisor request.
type cliCommand string
type cliVerb string
type cliFlag string

const (
	cliCommandState      cliCommand = "state"
	cliCommandDoctor     cliCommand = "doctor"
	cliCommandDaemon     cliCommand = "daemon"
	cliCommandSession    cliCommand = "session"
	cliCommandTask       cliCommand = "task"
	cliCommandCheckpoint cliCommand = "checkpoint"
)

const (
	cliVerbBackup    cliVerb = "backup"
	cliVerbVerify    cliVerb = "verify"
	cliVerbRestore   cliVerb = "restore"
	cliVerbServe     cliVerb = "serve"
	cliVerbStart     cliVerb = "start"
	cliVerbStatus    cliVerb = "status"
	cliVerbStop      cliVerb = "stop"
	cliVerbCreate    cliVerb = "create"
	cliVerbShow      cliVerb = "show"
	cliVerbComplete  cliVerb = "complete"
	cliVerbPublish   cliVerb = "publish"
	cliVerbCancel    cliVerb = "cancel"
	cliVerbEvents    cliVerb = "events"
	cliVerbPrune     cliVerb = "prune"
	cliVerbAdd       cliVerb = "add"
	cliVerbRun       cliVerb = "run"
	cliVerbRetry     cliVerb = "retry"
	cliVerbReply     cliVerb = "reply"
	cliVerbWait      cliVerb = "wait"
	cliVerbReconcile cliVerb = "reconcile"
)

const (
	cliFlagHome    cliFlag = "--home"
	cliFlagOut     cliFlag = "--out"
	cliFlagFrom    cliFlag = "--from"
	cliFlagTo      cliFlag = "--to"
	cliFlagFile    cliFlag = "--file"
	cliFlagTimeout cliFlag = "--timeout"
	cliFlagReason  cliFlag = "--reason"
	cliFlagAfter   cliFlag = "--after"
	cliFlagBackup  cliFlag = "--backup"
	cliFlagText    cliFlag = "--text"
)
