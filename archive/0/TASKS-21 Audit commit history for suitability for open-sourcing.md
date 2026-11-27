# TASKS-21: Audit commit history for suitability for open-sourcing

Status: closed

## Description

Sweep through the current source code and all historical commits looking for corporate or personal info not suitable for disclosure in an open source release.

## Closed (2026-11-21T20:52:45Z)

Completed the local disclosure audit and sanitized the identified ticket examples throughout reachable task history. ADR-0003 defines monthly checks of the public repository and how to respond to a leak. Publication is separate: verify that the host exposes only reviewed refs before changing visibility.
