package cli

func init() {
	commands.register(commandSpec{
		name: "close", min: 1, max: 1, numbered: true, options: bodyOptions(),
		usage:   "NUMBER " + bodyUsage,
		summary: "Close a task with a note, without merging or discarding work.",
		details: bodyHelp,
		run:     withTaskStore(storeRequirements{mutation: true, body: true}, addNote),
	})
}
