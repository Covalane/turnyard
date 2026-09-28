package transport

// These action names form the private CLI-to-supervisor wire protocol.
type Action string

const (
	actionPing              Action = "ping"
	actionDaemonStop        Action = "daemon.stop"
	actionSessionCreate     Action = "session.create"
	actionSessionShow       Action = "session.show"
	actionSessionComplete   Action = "session.complete"
	actionSessionPublish    Action = "session.publish"
	actionSessionCancel     Action = "session.cancel"
	actionTaskAdd           Action = "task.add"
	actionTaskRun           Action = "task.run"
	actionTaskShow          Action = "task.show"
	actionTaskReconcile     Action = "task.reconcile"
	actionTaskVerify        Action = "task.verify"
	actionEvents            Action = "events"
	actionCheckpointRestore Action = "checkpoint.restore"
)
