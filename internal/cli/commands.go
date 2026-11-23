package cli

import (
	"context"
	"fmt"
	"sort"
)

type optionKind uint8

const (
	flagOption optionKind = iota + 1
	valueOption
)

type commandHandler func(context.Context, string, arguments, IO) error

type commandSpec struct {
	name            string
	min, max        int
	options         map[string]optionKind
	numbered        bool // The first positional argument is a task number, if given.
	numberedAll     bool // All positional arguments are task numbers.
	passArgsAfter   int  // Pass through the remaining argv after this many positionals.
	validate        func(arguments) error
	positionalError string
	usage           string // Arguments and options, after the command name.
	summary         string // Shown in both the command list and the command's help.
	details         string
	hidden          bool
	run             commandHandler
}

type commandRegistry map[string]commandSpec

// Commands register explicitly in their own compiled source files. Registration
// does no repository/process work; runtime dispatch is a single map lookup.
var commands = commandRegistry{}

func (r commandRegistry) register(spec commandSpec) {
	if _, exists := r[spec.name]; exists {
		panic("duplicate task command: " + spec.name)
	}
	if (!spec.hidden && spec.summary == "") || spec.run == nil || spec.min < 0 || spec.max < spec.min {
		panic("incomplete task command: " + spec.name)
	}
	options := make(map[string]optionKind, len(spec.options))
	for name, kind := range spec.options {
		if kind < flagOption || kind > valueOption {
			panic("invalid task option: " + name)
		}
		options[name] = kind
	}
	spec.options = options
	r[spec.name] = spec
}

func (r commandRegistry) visible() []commandSpec {
	result := []commandSpec{}
	for _, spec := range r {
		if !spec.hidden {
			result = append(result, spec)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].name < result[j].name
	})
	return result
}

func unknownCommand(name string) error {
	return fmt.Errorf("unknown command: %s", name)
}
