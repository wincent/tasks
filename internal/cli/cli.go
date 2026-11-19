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

	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/process"
	"github.com/wincent/tasks/internal/store"
)

type IO struct {
	In       io.Reader
	Out, Err io.Writer
	Runner   process.Runner
}

type arguments struct {
	action      string
	positional  []string
	values      map[string][]string
	flags       map[string]bool
	prefix      store.Prefix
	inputPrefix store.Prefix
	taskNumber  int
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
		return a, unknownCommand(a.action)
	}
	literal := false
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "--" && !literal {
			literal = true
			continue
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" || negativeArgument.MatchString(arg) || literal {
			a.positional = append(a.positional, arg)
			continue
		}
		key, value, hasValue := strings.Cut(arg, "=")
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
		return arguments{action: "help", positional: []string{a.action}}, nil
	}
	if len(a.positional) < spec.min && spec.min != spec.max {
		return a, fmt.Errorf("%s expects at least %d positional argument(s)", a.action, spec.min)
	}
	if len(a.positional) < spec.min || len(a.positional) > spec.max {
		return a, fmt.Errorf("%s expects %d positional argument(s)", a.action, spec.max)
	}
	if len(a.values["--body"]) > 0 && len(a.values["--body-file"]) > 0 {
		return a, errors.New("--body and --body-file are mutually exclusive (--body-file is not allowed with argument --body)")
	}
	if spec.numbered && len(a.positional) != 0 {
		var err error
		a.inputPrefix, a.taskNumber, err = store.ParseNumber(a.positional[0])
		if err != nil {
			return a, err
		}
	}
	if spec.validate != nil {
		if err := spec.validate(a); err != nil {
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
	if a.inputPrefix != "" && a.inputPrefix != a.prefix {
		return &usageError{fmt.Errorf("unexpected task prefix %s; use %s or %d", a.inputPrefix, a.prefix.ID(a.number()), a.number())}
	}
	return run(ctx, cwd, a, streams)
}
