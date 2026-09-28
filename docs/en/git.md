# Git workflow

[User guide](README.md) · [中文](../zh/git.md)

Turnyard clones each repository at a pinned commit into an isolated workspace. A task may change only the worktrees marked `write` in `Work.scope.repositories`; the agent cannot change `.git` directly. On the host, Turnyard creates a `feature/<task-id>` branch, commits changes, and records base, head, and tree in the candidate. Each repository gets its own branch and commit.

The commit subject comes from `Work.commitMessage`; give it a useful title such as `feat: add Google OIDC sign-in` or `fix: bind OAuth state to browser`. It must be one line, at most 80 characters, with no control characters. If omitted, Turnyard derives a short subject from `objective`. The task ID remains in a `Turnyard-Task` commit-body trailer. Further commits for the same task get a numbered suffix such as `(revision 2)`. Turnyard does not automatically rewrite commits already published.

## Repository configuration

Use `local-git` with `path` for a local source. Use `remote-git` with `url` for a remote source:

```json
{"id":"app","type":"remote-git","url":"https://github.com/OWNER/REPO.git","commit":"0123456789abcdef0123456789abcdef01234567"}
```

The full 40-character commit SHA must already exist in the source. Seed and push an initial commit before using a newly created empty remote repository. HTTPS, SSH URLs, and absolute `file://` URLs are supported. URLs must not contain a password or token. Optional `pushUrl` selects the publication target; a remote source defaults to its `url`. A local source needs `pushUrl` before it can be published.

Private HTTPS repositories use the host's Git credential helper. SSH URLs use the host's SSH agent. Credentials do not enter JSON or the agent sandbox. With GitHub CLI, run `gh auth login` and `gh auth setup-git` first. Do not put tokens in task text, logs, or URLs.

## Publishing verified branches

A session with repositories must explicitly set `"git":{"localCommits":true,"remoteWrites":"none"}`. Set `"remoteWrites":"push"` to allow publication; this does not publish automatically. A repository-free session may omit `git` and still deliver files, images, or text. Git is a capability for versioned code candidates and publishing, not a requirement for every task. For code tasks, Turnyard creates feature commits on the host so checks bind to a specific revision; the sandbox cannot rewrite protected `.git` metadata.

After checks and human review, complete the session and explicitly publish:

```sh
bin/turnyard session complete <session-id>
bin/turnyard session publish <session-id> --timeout 600
```

Publication covers writable repositories in verified tasks. Before pushing, Turnyard rechecks the candidate branch head and tree. It refuses an existing remote branch at a different commit, never forces a push, and reads the remote ref after pushing to confirm the candidate head. Repeated calls return `already_published`. A cross-repository failure returns `partial` with per-branch status and error code, and the CLI exits nonzero. Inspect the failure and repeat the same command to reconcile branches already published.

Session completion establishes only that configured checks and declared deliverables passed. Publication is not a code review, pull request, merge, or deployment. Remote protection and permissions can still reject the write; local candidates and events remain available for investigation.
