# Folder pagination investigation — 2026-09-10

The missing archive results were caused by treating one response as an entire
folder. A request count is an upper bound. **A short page does not imply EOF.**

Read-only probes made identical requests through upstream libgm v0.6.4 and the
pinned cursor-capable fork. Both returned:

| Request | Rows returned | Continuation |
|---|---:|---|
| inbox, count 500 | 10 | absent |
| archive, count 500 | 284 | present |

The archive continuation identified the last returned thread and timestamp.
The requested 500 did not force the phone to return 500 rows. The reason for
that smaller response (such as a response-size budget) was not established.
There was no transport-version difference in the returned ID sets.

Using the fork’s `ListConversationsWithCursor(25, folder, cursor)` reached 646
unique archived threads across 26 pages: 25 full pages and a terminal page of
21 records with no cursor. Every returned row had archived status. The candidate
CLI reproduced the result via `gmcli --json sync` and verified the original
archive test targets, including the groups that had produced transient active
readbacks. Individual status, archive membership, and absence from inbox agreed.

The cached Google Messages web bundle uses action 1 for conversation lists,
request field 2 for count, field 4 for folder, and field 5 for its continuation.
The response also exposes continuation in field 5. The bundle’s usual page size
is 25. Archive uses action 15 with conversation action 4 and top-level thread ID;
unarchive uses conversation action 3. No unarchive command is added by this work.

The dependency is pinned to
[MaxGhenis/gmessages 0e43542](https://github.com/MaxGhenis/gmessages/commit/0e43542dfa0e0b97e410f185a5842e8740106099),
which exposes cursor requests over the existing transport. The old notes’
`a6d7f98` pin is no longer resolvable through Go; the current fork tip has the
same cursor API. No private transport copy or unsafe access to internal fields
is needed.

## Contracts and remaining limits

- Follow the response cursor, including after a short page. Stop with explicit
  partial coverage on repeated cursors, an opaque undecoded cursor, request-budget
  exhaustion, unexpected folder status, or an error.
- A nil cursor is the protocol’s terminal marker. It does not make multiple
  pages an atomic snapshot. Concurrent phone activity can alter results.
- Bound output separately from enumeration. A display limit cannot define the
  reviewed universe for a cleanup task.
- A completed folder scan says nothing about historical-message coverage.
  FetchMessages can return synchronization records for other threads; count
  processed records separately from target-chat history. A backfill starting
  at the oldest cached message does not establish that newer cached history
  has no gaps.
- A write acknowledgment can precede status propagation. Poll readback without
  blindly repeating writes, and independently verify folder membership for a
  batch. Success describes observed state, not immunity from future messages.
- No unbreakable 500-thread Google limit was found. The tested implementation
  uses pages of 1–100 and a configurable page budget; exhaustion is an error.

Raw message exports and authentication material remain outside the repository.
