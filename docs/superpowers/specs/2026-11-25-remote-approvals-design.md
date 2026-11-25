# Remote approvals from a paired phone (remote-approvals)

Status: IDEA spec r3, for review (subject `remote-approvals-spec`). Not built, and **later**: the
note places it after a read-only `agentbus web`, which does not exist yet. Base: origin/dev
`af527c19`. Author: architect, from master's directive (cmd `1795559518593-0`, task
`specs-from-person-docs`). This is idea 4 of 4 from the person's note "ideas borrowed from PiBot",
item 4 there. The note's references are PiBot `packages/runner/src/phone-policy.ts` and
`packages/link/src/pairing/phone-control.ts` (Apache-2.0), used for the idea only. **New
dependency, named by the note:** `github.com/flynn/noise`. Ed25519 is the Go standard library.
The questions for the person are last (§6).
r2: foureyes's reject of r1 (verdict `dn4478y7pdk4`). §3 states who can read the issuer key
today, and that a phone answer on the ledger is no stronger than `typed=yes`. A grant is renewed
only at the desk, so 24 h is a hard bound. The ask's state is checked inside the answer script.
Also reviewer-codex's reject of r1 (verdict `dn4478ybr8t7`). Checking, spending the nonce and
answering are one script, so nothing changes between them and nothing is spent unapplied. Grants
and approvals are scoped to one project and signed over a fixed binding. Noise gets its own X25519
keys, bound to the signing keys at pairing.
r3: reviewer-codex's reject of r2 (verdict `dn449ev49sz9`). The script checks the grant's expiry
too, and compares the stored approval with the signed values, never the stored ones alone.

## 1. Today

The person decides at the desk, in busmon: an ask's answer, a merge ask, master's promotion. The
phone only receives. Notifications go out through ntfy or Discord, one way, and carry no secret
and no answer option (notifications spec, "Never"). That spec left two-way for later, behind a
`Receiver` interface. ntfy and Discord are third parties, so they cannot carry a decision that
the bus must trust.

## 2. The idea

A phone the person pairs once at the desk can answer asks from anywhere. The channel is end to
end between the phone and this host. Nothing in it rests on a third party.

**Keys, two pairs on each side.** Signing and transport are separate keys; nothing converts one
into the other.

- The host: an Ed25519 **issuer** key, which signs grants, and an X25519 **transport** key, Noise's
  static key. Both in `~/.config/agentbus/` (0600). Every Claude Code role gets a deny on that
  directory's two files, as it does for `notify.toml` (`roles.toml:168`). That rule binds only
  Claude Code (§3). With idea 3 built, a sandboxed role cannot read them at all.
- Each phone: an Ed25519 **device** key, which signs approvals, and an X25519 transport key. Both
  are made on the phone and never leave it.

**Pairing, at the desk only, for one project.** busmon shows a QR with a one-time code, the project
name, and the host's two public keys. The phone sends its two public keys, with a signature by its
device key over the code, the project and both of its keys. The person confirms one fingerprint
covering both phone keys in busmon, by typing it. The host records the device for that project.
A phone used on two projects is paired twice.

**Grants.** For a paired device, the host signs a grant:
`{issuer, project, device, device_transport, capabilities, revision, issued_at, expires_at}`,
where `expires_at ≤ issued_at + 24 h`. The phone shows the grant on every request. The host
verifies the signature, the project, the device, that the Noise session's remote static key is
`device_transport`, the expiry and the revision. The capability is checked on every request, reads
included: a grant without `answer` cannot answer, and one without `read` cannot read. **The
devices and the revision live in a file beside the keys**, `~/.config/agentbus/phone/<project>`
(0600, under the same deny), not in Redis, where any role with data access could add a device.
Redis keeps a copy of the revision in `{p}:phone`, with the pending approvals, so the answer script
can check it atomically. Both must match the grant: a role that lowers the Redis copy revives
nothing, and one that raises it only refuses phone answers. Revocation bumps the `revision`:
every older grant fails at once. `agentbus phone revoke` does that, and so does busmon. **A grant
is renewed only at the desk:** busmon lists the paired devices, and the person renews one with a
keypress, for the same capabilities. The phone cannot renew its own grant. So a lost phone stops
working within 24 h even if nobody notices, and revocation stops it at once. The cost: away from
the desk for more than a day, the phone answers nothing (§6 Q3).

**Capabilities, first build:** read what busmon's For you shows, and answer an ask. Nothing
else. No directive, no merge, no promotion, and nothing typed into a pane (§6 Q1).

**One action, one use.** To answer, the phone fetches a pending approval: a nonce and a
deadline at most 120 s ahead, from Redis `TIME`, stored in `{p}:phone`. The phone signs a fixed
binding: the tag `agentbus-phone-approval-v1`, then issuer key, project, device key, capability,
ask id, question id, chosen option, the hash of the ask's definition, nonce and deadline, in that
order, each length-prefixed. The verifier checks the grant and this signature, then calls **one
Lua script that checks and writes together**:

- the grant's revision is still `{p}:phone`'s copy (the verifier checked the file's before);
- the nonce is pending, and its stored record (project, device, ask, question, option,
  definition hash, deadline) equals the values the phone signed, which the verifier passes in;
- by Redis `TIME`, neither the signed deadline nor the grant's `expires_at`, also passed in, has
  passed. A pending approval's deadline is also never set past the grant's expiry;
- the ask is open, **not replaced**, and its definition hash still matches. `askAnswer` today
  follows a replaced ask to its replacement when the question was carried
  (`bus/asks.go:311-316`). A phone answer refuses instead of following;
- then it spends the nonce, records the reply id against it, and writes the answer.

Nothing can change between the check and the write, and a refusal spends nothing. A crash
before the script leaves the approval usable until its deadline. A crash after it leaves the
answer written. The phone may retry the same signed approval: once spent, the script returns
the reply id it already wrote and writes nothing.

**The answer it writes.** The same reply `Bus.Answer` (`bus/asks.go:464`) writes, through a phone
variant of its script: as the person, with `typed=yes` and an additive `via=phone:<device>`, a new
field like the retro spec's `qid`. An agent and busmon then see where it came from (§6 Q2).

**Transport.** First LAN only: the host's `agentbus web` listener and the phone talk Noise IK
(`flynn/noise`, `DH25519`), the phone as initiator. The responder's static key is the host's
X25519 transport key, pinned from the QR at pairing. Later, a relay may forward the
Noise traffic and see only ciphertext (§6 Q4). Once this is built, it replaces ntfy and Discord as
the channel for decisions. They stay as one-way bells.

## 3. Where it sits

The verifier is a host process, the `agentbus web` service, running as the person's user. It
is the only holder of the issuer key's use. It is not an agent. Agents never see a grant, a
device key or a pending approval. A grant whose capability list grows needs a new pairing step at
the desk: a grant is never widened remotely.

**What the key protects, and what it does not.**

- **The phone channel, not the ledger.** A phone answer on the bus is no stronger than
  `typed=yes` today. Any role with data access can `XADD` a reply entry carrying `typed=yes` and
  `via=phone:<x>` itself. The issuer key stops a forged phone action from reaching the verifier;
  it does not make a reply on the stream trustworthy. That limit is the existing one, and this
  spec does not change it.
- **Who can read the host's keys today.** Any process of the person's user. The deny is a Claude Code
  rule, so it binds only Claude roles. `ui-kimi` and `ui-grok` hold no such rule, and
  `reviewer-codex`'s `auto_review` escalations run outside its read-only sandbox (the
  bounded-auto spec, §3). Those roles can read the keys unless idea 3 sandboxes them, and idea 3
  starts as a cohort trial. With the key, a role could sign grants for a device of its own.
- The person's own shell is not confined.

## 4. Build steps (later, after the read-only web)

0. A spike: `flynn/noise` IK (`DH25519`) between a Go host and a phone client. The phone app is
   the open cost: a PWA with WebCrypto Ed25519 and X25519, or a small native app.
1. Keys, pairing, grants, revocation, and `agentbus phone pair/list/revoke`.
2. Pending approvals, the signed binding, and the one script that checks, spends and answers,
   with `via`. `{p}:phone` is a new key kind (`bus/keykinds.go`), and a reset keeps it, like the
   asks. Nothing in it is trusted alone: a pending approval is worth nothing without the phone's
   signature.
3. LAN transport. A relay is a separate spec.

## 5. Tests, and what this does not do

**Tests:** a grant past its expiry, with an older revision, from another device, or with a bad
signature is refused. A grant with `expires_at` more than 24 h after `issued_at` is never
signed. An approval older than 120 s, a replayed one, one whose ask changed since, and one for
another option, another project, or with a grant lacking `answer`, are each refused. A read with a
grant lacking `read` is refused. A grant or approval signed for project A is refused on project B.
Races, each against the real script: the ask replaced, a revocation, the approval's deadline
passing, and the grant's expiry passing (tested apart from the deadline), each between the verifier's checks and the script, refuse and spend nothing. A stored approval whose record differs from the signed values in any field is refused. Two concurrent
submissions of one approval write one reply; a retry after it returns the same reply id. A
Noise session whose remote static key is not the grant's `device_transport` is refused.
An approval for an ask replaced since, even with the question carried, is refused by the answer
script and writes nothing. A phone request to renew its own grant is refused. One valid answer writes exactly one reply, with `typed=yes` and
`via`. Revocation refuses the next action. An agent process cannot run `phone pair` or `phone
revoke` (`agentcaller.Refuse`).

**Not doing:** no third-party channel for decisions, no pane input from the phone, no remote
widening of a grant, and no relay in the first build.

## 6. Questions for the person

**Q1. What may the phone do in the first build?**
- **(A) recommended: answer asks, and read For you.**
- (B) also send a directive to master.
- (C) also merges and the promotion of `main`.

**Q2. Does a phone answer count wherever your typed answer counts** (the retro spec's approval,
master's promotion ask)?
- **(A) recommended: yes, marked `via=phone`.** busmon shows the mark.
- (B) yes, except master's promotion of `main`, which stays at the desk.

**Q3. Grant lifetime and renewal.**
- **(A) recommended: 24 h, renewed only at the desk.** A lost phone stops within a day; away
  longer, the phone answers nothing.
- (B) 8 h, renewed only at the desk.
- (C) 24 h, and the phone may renew itself with a PIN you type on it, up to 7 days from the last
  desk renewal. A stolen phone whose PIN is known keeps working for up to 7 days unless revoked.

**Q4. Away from home.**
- **(A) recommended: LAN only first,** with a relay as a later spec.
- (B) a relay in the first build.
