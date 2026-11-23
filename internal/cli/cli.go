// Package cli adapts terminal/process I/O to the standalone task store.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/process"
	"github.com/wincent/tasks/internal/query"
	"github.com/wincent/tasks/internal/store"
)

type IO struct {
	In       io.Reader
	Out, Err io.Writer
	Runner   process.Runner
}

type taskArgument struct {
	prefix store.Prefix
	number int
}

type arguments struct {
	action     string
	correction commandCorrection
	positional []string
	values     map[string][]string
	flags      map[string]bool
	prefix     store.Prefix
	tasks      []taskArgument
	query      *query.Expression
}

// Match the prior argument parser's negative-number positional handling;
// number validation, rather than option parsing, supplies the useful error.
var negativeArgument = regexp.MustCompile(`^-(?:[0-9]+|[0-9]*\.[0-9]+)$`)

func parse(args []string) (arguments, error) {
	a := arguments{values: map[string][]string{}, flags: map[string]bool{}}
	if len(args) == 0 {
		return a, nil
	}
	a.action = args[0]
	if a.action == "--help" || a.action == "-h" {
		a.action = "help"
		return a, nil
	}
	spec, ok := commands[a.action]
	if !ok || a.action == "" {
		spec, ok = commands.closest(a.action)
		if !ok {
			return a, unknownCommand(a.action)
		}
		a.correction = commandCorrection{from: a.action, to: spec.name}
		if spec.autoCorrect == nil {
			return a, a.correction.suggestion()
		}
		a.action = spec.name
	}
	literal := false
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if spec.passArgsAfter > 0 && len(a.positional) == spec.passArgsAfter {
			if arg == "--" && !literal {
				i++
			}
			a.positional = append(a.positional, args[i:]...)
			break
		}
		if arg == "--" && !literal {
			literal = true
			continue
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" || negativeArgument.MatchString(arg) || literal {
			a.positional = append(a.positional, arg)
			continue
		}
		key, value, hasValue := strings.Cut(arg, "=")
		if key == "-q" && spec.options["--query"] == valueOption {
			key = "--query"
		}
		if key == "-v" && spec.options["--verbose"] == flagOption {
			key = "--verbose"
		}
		if (key == "--help" || key == "-h" || spec.options[key] == flagOption) && !hasValue {
			a.flags[key] = true
			continue
		}
		if spec.options[key] != valueOption {
			return a, fmt.Errorf("unrecognized argument: %s", arg)
		}
		if !hasValue {
			i++
			if i == len(args) {
				return a, fmt.Errorf("%s requires a value", key)
			}
			value = args[i]
		}
		if len(a.values[key]) != 0 {
			return a, fmt.Errorf("%s may only be supplied once", key)
		}
		a.values[key] = append(a.values[key], value)
	}

	if a.flags["--help"] || a.flags["-h"] {
		return arguments{action: "help", correction: a.correction, positional: []string{a.action}}, nil
	}
	if a.correction.from != "" && !spec.autoCorrect(a) {
		return a, a.correction.suggestion()
	}
	if spec.positionalError != "" && (len(a.positional) < spec.min || len(a.positional) > spec.max) {
		return a, errors.New(spec.positionalError)
	}
	if len(a.positional) < spec.min && spec.min != spec.max {
		return a, fmt.Errorf("`tasks %s` expects at least %d positional argument(s)", a.action, spec.min)
	}
	if len(a.positional) < spec.min || len(a.positional) > spec.max {
		return a, fmt.Errorf("`tasks %s` expects %d positional argument(s)", a.action, spec.max)
	}
	if len(a.values["--body"]) > 0 && len(a.values["--body-file"]) > 0 {
		return a, errors.New("--body and --body-file are mutually exclusive (--body-file is not allowed with argument --body)")
	}
	if spec.validate != nil {
		if err := spec.validate(a); err != nil {
			return a, err
		}
	}
	var taskArgs []string
	switch {
	case spec.numberedAll:
		taskArgs = a.positional
	case spec.numbered && len(a.positional) != 0:
		taskArgs = a.positional[:1]
	}
	for _, arg := range taskArgs {
		prefix, number, err := store.ParseNumber(arg)
		if err != nil {
			return a, err
		}
		a.tasks = append(a.tasks, taskArgument{prefix, number})
	}
	if len(a.values["--query"]) != 0 {
		var err error
		a.query, err = query.Parse(a.value("--query"), time.Now(), store.ValidStatus)
		if err != nil {
			return a, err
		}
	}
	return a, nil
}

func (a arguments) value(key string) string {
	if v := a.values[key]; len(v) > 0 {
		return v[0]
	}
	return ""
}

func (a arguments) number() int {
	if len(a.tasks) == 0 {
		return 0
	}
	return a.tasks[0].number
}

func (a arguments) numbers() []int {
	numbers := make([]int, len(a.tasks))
	for i, task := range a.tasks {
		numbers[i] = task.number
	}
	return numbers
}

func encode(out io.Writer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

// Run operates in an explicit directory and never changes the process cwd.
func Run(ctx context.Context, cwd string, args []string, streams IO) (err error) {
	if streams.Out == nil {
		streams.Out = io.Discard
	}
	if streams.Err == nil {
		streams.Err = io.Discard
	}
	if streams.In == nil {
		streams.In = strings.NewReader("")
	}
	a, err := parse(args)
	if err != nil {
		return &usageError{err}
	}
	if c := a.correction; c.from != "" {
		if _, err := fmt.Fprintf(streams.Err, "warning: subcommand `%s` does not exist; using `%s` instead.\n", c.from, c.to); err != nil {
			return err
		}
	}
	run := commands[a.action].run
	if a.action == "help" {
		return run(ctx, cwd, a, streams)
	}
	repo, err := git.Open(cwd)
	if err != nil {
		return err
	}
	a.prefix, err = store.LoadPrefix(repo)
	repo.Close()
	if err != nil {
		return err
	}
	seen := make(map[int]bool, len(a.tasks))
	for _, task := range a.tasks {
		id := a.prefix.ID(task.number)
		if task.prefix != "" && task.prefix != a.prefix {
			return &usageError{fmt.Errorf("unexpected task prefix %s; use %s or %d", task.prefix, id, task.number)}
		}
		if seen[task.number] {
			return &usageError{fmt.Errorf("duplicate task number: %s", id)}
		}
		seen[task.number] = true
	}
	return run(ctx, cwd, a, streams)
}
