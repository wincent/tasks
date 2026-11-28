# TASKS-78: Support OSC 7501 program status

Status: done

## Description

[OSC 7501](https://mitchellh.com/writing/program-status-osc7501) escape sequences are supported by tools like Pi (can be forced on or off with
`PI_PROGRAM_STATUS=1` and `PI_PROGRAM_STATUS=0`, respectively, but
otherwise defaults to autodetecting the terminal capability; note that it doesn't work out-of-the-box inside tmux, so I'm actually using a small extension to emit the codes in Pi, and I then rely on tmux passthrough to let the sequences git to kitty, which added support in 0.50.0), and I'm using kitty's `tab.program_status` in the tab `tab_title_template` and have these status icons show up there.

The [implementation that I'm using in the Pi extension](https://github.com/wincent/wincent-agent-plugins/blob/ee74c2043f1f08d1ebad037c2b28668594f62b0e/pi/extensions/program-status.ts) is extremely simple, with no sophisticated attempts at coordination between processes (ie. it's "last-write-wins"). It does nothing in non-TUI sessions, in subagents, if the TTY is not interactive, or when it's not inside tmux (ie. when `TMUX` is not set in the environment).

I'd like `tasks` to do something similar:

- If the terminal is interactive; and:
- We're in tmux; and:
- The user has issued a `tasks run` or `tasks go` command; then:

We can use OSC 7501 codes to indicate `'working'`, `'done'`, or `'error'`. Please consider whether any other states would be useful; the three I just mentioned are the ones that seem most likely to be worthwhile, at a glance.
