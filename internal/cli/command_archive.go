package cli

import "context"

func init() {
	commands.register(commandSpec{
		name: "archive", min: 1, max: 1, numbered: true,
		usage:   "NUMBER",
		summary: "Hide a task from the default working set.",
		details: `Move one ticket to the archive in a local tasks-branch commit. The task's
status, artifacts, checkout and session are unchanged. A running task cannot
be archived. Use 'tasks unarchive NUMBER' to restore the ticket.`,
		run: withTaskStore(storeRequirements{mutation: true, taskLock: true}, moveTask),
	})
	commands.register(commandSpec{
		name: "unarchive", min: 1, max: 1, numbered: true,
		usage:   "NUMBER",
		summary: "Restore an archived task to the working set.",
		details: `Move one ticket back to the tasks-branch root in a local commit. The task's
status, artifacts, checkout and session are unchanged.`,
		run: withTaskStore(storeRequirements{mutation: true}, moveTask),
	})
}

func moveTask(_ context.Context, _ string, a arguments, streams IO, invocation storeInvocation) error {
	task, err := invocation.session.MoveTask(a.prefix, a.number(), a.action == "archive")
	if err != nil {
		return err
	}
	action := "Unarchived"
	if task.Archived {
		action = "Archived"
	}
	return taskResult(streams.Out, action, task.ID(), task.Title, task.Path)
}
