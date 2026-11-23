package cli

func init() {
	commands.register(commandSpec{
		name: "comment", autoCorrect: usesBodyEditor, min: 1, max: 1, numbered: true, options: bodyOptions(),
		usage:   "NUMBER " + bodyUsage,
		summary: "Add a Markdown comment to a task.",
		details: bodyHelp,
		run:     withTaskStore(storeRequirements{mutation: true, body: true}, addNote),
	})
}
