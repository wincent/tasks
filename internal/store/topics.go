package store

import (
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/wincent/tasks/internal/git"
)

// RecordedTopics checks both the committed and working copies. A local edit
// must not hide a destination already recorded in shared history.
func (store *Store) RecordedTopics() (map[string]string, error) {
	head, err := store.Head()
	if err != nil {
		return nil, err
	}
	topics := make(map[string]string)
	for _, revision := range []git.ID{head.ID, {}} {
		if store.Prospective() && revision == (git.ID{}) {
			break
		}
		entries, err := store.entries("", revision)
		if err != nil {
			return nil, err
		}
		archived, err := store.archiveEntries(revision)
		if err != nil {
			return nil, err
		}
		for _, entry := range append(entries, archived...) {
			// Scan all ticket prefixes; the implementation prefix can change.
			match := ticketName.FindStringSubmatch(filepath.Base(entry.name))
			if match == nil {
				continue
			}
			number, err := strconv.Atoi(match[2])
			if err != nil {
				return nil, err
			}
			if !inTicketLocation(entry.name, number) {
				continue
			}
			content, err := store.readTaskEntry(entry, false)
			if err != nil {
				return nil, fmt.Errorf("read topic in %s: %w", entry.name, err)
			}
			topic, err := Topic(string(content))
			if err != nil {
				return nil, fmt.Errorf("read topic in %s: %w", entry.name, err)
			}
			if topic != "" {
				topics[topic] = match[1] + "-" + match[2]
			}
		}
	}
	return topics, nil
}
