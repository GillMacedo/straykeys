# 0004: Refuse input that can't be trusted

## Context
A CI check that prints "No problems" for an empty file, an error page, or the output of a failed command is worse than no check. Two earlier tools in this account passed their own tests and still reported failed input as all clear.

## Decision
Exit 2 with nothing on stdout for any config or Go package that can't be fully read, and for a file none of whose top-level keys belong to the struct. A YAML file without a final line break, which may be cut mid-line, gets a `no-final-newline` error and is still checked, because many real config files lack the newline.

## Consequences
A YAML file cut exactly at a line break can't be told from a complete one. The README says so, and the header shows how many keys were checked.
