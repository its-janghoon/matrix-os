# Working in this repository

## Commit and pull-request attribution

Every commit's author AND committer is:

```
Janghoon Lee <44862514+its-janghoon@users.noreply.github.com>
```

That is the GitHub noreply form for the account, so the commit is attributed to a
person and linked to their profile without publishing an email anywhere.

**No `Co-Authored-By` trailer, ever**, for any address including that one. An
earlier version of this file asked for one on every commit, which is why 160 of
them carry a trailer naming a model. Attribution belongs in the author field,
where git already keeps it; a trailer only adds a second answer to a question
that already has one.

Never put a model name, model identifier, or version string into a commit
message, a pull-request title or body, a code comment, or anything else that
lands in this repository. Those belong in chat and nowhere else. The same goes
for an agent's own scratch files: `.agents/` and `.tasks/` are gitignored and
stay local.

## Prose

Short dashes, never long ones. That applies to commit messages, documentation
and code comments alike.

## Before opening a pull request

`make` generates the Protocol Buffers stubs and builds the Go daemon; `make test`
and `make vet` run over `services/core`; `make fmt` fails on anything not
gofmt-clean. The web app uses **yarn**, not npm. Run the gates rather than
reasoning about them — [`handoff.md`](handoff.md) records what the network is
doing and the two traps that make a healthy quiet chain look broken.
