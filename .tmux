#!/bin/bash

set -e

SCRIPT=${BASH_SOURCE[0]}
[[ $SCRIPT == /* ]] || SCRIPT=$PWD/$SCRIPT
REPO_ROOT=${SCRIPT%/*}

SESSION=tasks

if tmux has-session -t="$SESSION" 2> /dev/null; then
  tmux attach -t "$SESSION"
  exit
fi

# 1. Main window
tmux new-session -d -s "$SESSION" -n main -x $(tput cols) -y $(tput lines)

# 2. Secondary window (tasks)
tmux new-window -t "$SESSION" -c "$REPO_ROOT/tasks" -n tasks

tmux attach -t "$SESSION":main
