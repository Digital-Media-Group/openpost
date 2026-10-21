### Added

- Notify the people who can approve when a workflow parks a draft for approval, and notify them and the author when that wait expires. Both transitions were silent before: a draft could sit in the approval queue, and later disappear from it, without anyone being told. The notification links straight to the waiting run, which now opens on that run. The audience is the roles that satisfy the approve level — administrators and clients — derived from the access matrix rather than restated, so a client, who can decide an approval without being able to administer the workspace, is told about the wait that is theirs to resolve. The author is skipped when others can decide, and told when nobody else can.
- Remind the approvers three days before a wait expires. An expiry notice alone arrives thirty days after anyone last thought about the draft, which is too late to act on; the reminder is the only notice that arrives while the reader can still do something about it.
- Resolve the approval notices once the wait is decided. Approving or cancelling left every other approver holding an item that said "waiting for your approval" with a button that answered with an error; the row now keeps its place in the history but stops claiming something untrue.

### Changed

- Collapse the two approval notification topics into one `workflow_approval` topic with three states. The preferences page offered two switches for one mechanism, and neither described what actually happened.

### Fixed

- Emit approval notifications after the transaction that changes the run, not inside it. A failed notification write used to roll the park back: a recipient whose stored preferences would not decode, or a workspace lookup that found no row, left the run behind its lease, so the sweep re-enqueued it and the step replayed and failed again — a loop in which the run never parked. Notification failures are now logged and the run keeps its state.
- Expire stale approvals per run instead of per batch. `expireApprovals` returned on the first error, so one run that could not be expired stopped every other wait in the batch from expiring, once a minute.
- Restore the revision to the expiry compare-and-set. The guard had been narrowed to the state alone, which still yields one winner but no longer matches the contract that a writer only replaces the row it read.
- Carry the workspace in the notification link. The run inspector resolves a run against the workspace the viewer is in, so an approver arriving from another workspace landed on an empty inspector.
- Read the deep link reactively. The requested run was captured once at mount, so clicking a second approval notification while the page was already open did nothing.
