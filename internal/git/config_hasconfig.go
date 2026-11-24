package git

import (
	"errors"
	"path/filepath"
	"strings"
	"time"
)

const hasconfigURLPrefix = "hasconfig:remote.*.url:"

// A record keeps its file origin and scope even when evaluated in an application
// backend. Command captures and ordinary per-operation files have distinct loaders.
type hasconfigRecord struct {
	name, origin, backend string
	value                 *string
	level                 int
	depth                 uint
	conditionalURL        bool
}

type hasconfigSource struct {
	command bool
	level   int // zero preserves root record levels; includes inherit their parent
	records []hasconfigRecord
	load    func(string) ([]hasconfigRecord, error)
}

type hasconfigEvaluation struct {
	work                   *configurationWork
	matches                func(string, string) (bool, error)
	urls                   []string
	scan                   bool
	protectedOwnership     bool
	rejectCommandOwnership bool
}

// Unlike protected bare policy, this scan follows active repository conditions.
// It does not apply any records or extend the protected scope of command includes.
func (evaluation *hasconfigEvaluation) validateOwnership(sources []hasconfigSource) error {
	evaluation.scan = true
	for _, source := range sources {
		if source.command {
			continue
		}
		if err := evaluation.walk(source, source.records, 0, source.level, false, nil); err != nil {
			return err
		}
	}
	if evaluation.protectedOwnership {
		return conditionalOwnershipPolicyError()
	}
	return nil
}

func hasconfigRemoteURL(name string) bool {
	return strings.HasPrefix(name, "remote.") && strings.HasSuffix(name, ".url") && len(name) >= len("remote..url")
}

func hasconfigMatches(pattern string, urls []string) (bool, error) {
	if len(urls) == 0 {
		return false, nil
	}
	compiled, err := compileConfigurationWildmatch(pattern, false)
	if err != nil || compiled == nil {
		return false, err
	}
	for _, url := range urls {
		if compiled.MatchString(configurationWildmatchSubject(url, false)) {
			return true, nil
		}
	}
	return false, nil
}

func (evaluation *hasconfigEvaluation) evaluate(sources []hasconfigSource) ([]hasconfigRecord, error) {
	var result []hasconfigRecord
	err := evaluation.evaluateEach(sources, func(record hasconfigRecord) error {
		// Only the final owned output copies value pointers. Neither a caller
		// nor another occurrence of this include may mutate a borrowed value.
		if record.value != nil {
			value := *record.value
			record.value = &value
		}
		result = append(result, record)
		if evaluation.work != nil {
			evaluation.work.copies++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Scan the complete graph first, forcing hasconfig edges even when they cannot
// match. This both discovers forward URLs and forbids URLs in every hasconfig
// closure, including ownership resets regardless of the URL match. Other
// conditions still require a matching context: their false targets stay unread.
// The effective pass then visits matching edges in source order, without making
// intermediate flattened slices. The visitor must not mutate borrowed values.
func (evaluation *hasconfigEvaluation) evaluateEach(sources []hasconfigSource, visit func(hasconfigRecord) error) error {
	if work := evaluation.work; work != nil {
		work.evaluations++
		start := time.Now()
		defer func() { work.evaluationTime += time.Since(start) }()
	}
	evaluation.scan, evaluation.urls = true, nil
	for _, source := range sources {
		if err := evaluation.walk(source, source.records, 0, source.level, false, nil); err != nil {
			return err
		}
	}
	evaluation.scan = false
	for _, source := range sources {
		if err := evaluation.walk(source, source.records, 0, source.level, false, visit); err != nil {
			return err
		}
	}
	return nil
}

func (evaluation *hasconfigEvaluation) walk(source hasconfigSource, records []hasconfigRecord, depth uint, level int, forbidden bool, visit func(hasconfigRecord) error) error {
	if depth > 10 {
		return errors.New("Git includes exceed the maximum nesting depth")
	}
	for _, record := range records {
		// Change only the stack-local record, never the source slice or value.
		if depth > 0 || level != 0 {
			record.level = level
		}
		record.depth, record.conditionalURL = depth, forbidden
		if hasconfigRemoteURL(record.name) {
			if forbidden {
				return errors.New("remote URLs cannot be configured directly or indirectly inside hasconfig includes")
			}
			if evaluation.scan && record.value != nil {
				evaluation.urls = append(evaluation.urls, *record.value)
			}
		}
		if record.name == "safe.directory" {
			if forbidden {
				evaluation.protectedOwnership = true
			}
			if evaluation.rejectCommandOwnership && source.command {
				return commandOwnershipPolicyError()
			}
		}
		if !evaluation.scan && visit != nil {
			if err := visit(record); err != nil {
				return err
			}
		}
		matched, hasconfig := record.name == "include.path", false
		if strings.HasPrefix(record.name, "includeif.") && strings.HasSuffix(record.name, ".path") {
			condition := strings.TrimSuffix(strings.TrimPrefix(record.name, "includeif."), ".path")
			hasconfig = strings.HasPrefix(condition, hasconfigURLPrefix)
			var err error
			if hasconfig {
				matched = evaluation.scan
				if !matched {
					matched, err = hasconfigMatches(strings.TrimPrefix(condition, hasconfigURLPrefix), evaluation.urls)
				}
			} else if evaluation.matches != nil {
				matched, err = evaluation.matches(condition, record.origin)
			}
			if err != nil {
				return err
			}
		}
		if !matched {
			continue
		}
		if record.value == nil {
			return errors.New("active Git include.path requires a value")
		}
		path := *record.value
		if !filepath.IsAbs(path) && !strings.HasPrefix(path, "~/") {
			if record.origin == "" {
				return errors.New("relative command Git include has no originating configuration file")
			}
			path = filepath.Dir(record.origin) + string(filepath.Separator) + path
		}
		included, err := source.load(path)
		if err != nil {
			return err
		}
		if err := evaluation.walk(source, included, depth+1, record.level, forbidden || hasconfig, visit); err != nil {
			return err
		}
	}
	return nil
}
