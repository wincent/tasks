# Contributing

Install the [build prerequisites](README.md#prerequisites) and make sure `dprint` is on your `PATH`.

From the repository root, run:

```sh
go test ./...
go vet ./...
bin/check-format
```

Run `bin/format` to format Go and Markdown files.
