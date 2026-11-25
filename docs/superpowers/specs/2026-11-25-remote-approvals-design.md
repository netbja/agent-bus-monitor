# Remote approvals from a paired phone (remote-approvals)

Status: IDEA spec r1, for review (subject `remote-approvals-spec`). Not built, and **later**: the
note places it after a read-only `agentbus web`, which does not exist yet. Base: origin/dev
`af527c19`. Author: architect, from master's directive (cmd `1795559518593-0`, task
`specs-from-person-docs`). This is idea 4 of 4 from the person's note "ideas borrowed from PiBot",
item 4 there. The note's references are PiBot `packages/runner/src/phone-policy.ts` and
`packages/link/src/pairing/phone-control.ts` (Apache-2.0), used for the idea only. **New
dependency, named by the note:** `github.com/flynn/noise`. Ed25519 is the Go standard library.
The questions for the person are last (§6).

## 1. Today

The person decides at the desk, in busmon: an ask's answer, a merge ask, master's promotion. The
phone only receives. Notifications go out through ntfy or Discord, one way, and carry no secret
and no answer option (notifications spec, "Never"). That spec left two-way for later, behind a
`Receiver` interface. ntfy and Discord are third parties, so they cannot carry a decision that
the bus must trust.

## 2. The idea

A phone the person pairs once at the desk can answer asks from anywhere. The channel is end to
end between the phone and this host. Nothing in it rests on a third party.

**Keys.** On first use the host creates an Ed25519 issuer key, `~/.config/agentbus/issuer.key`
(0600). Every role gets a Claude Code deny on that path, as it does for `notify.toml`
(`roles.toml:159`). With idea 3 built, a sandboxed role cannot read it at all. Each phone makes
its own Ed25519 device key, which never leaves the phone.

**Pairing, at the desk only.** busmon shows a one-time code and the host's public key as a QR.
The phone sends its device public key. The person confirms the key's fingerprint in busmon, by
typing it. The host records the device.

**Grants.** For a paired device, the host signs a grant:
`{issuer, device, capabilities, revision, issued_at, expires_at}`, where `expires_at ≤ issued_at
+ 24 h`. The phone shows the grant on every action. The host verifies the signature, the
device, the expiry and the revision on every action. Revocation bumps the host's `revision`:
every older grant fails at once. `agentbus phone revoke` does that, and so does busmon. A new
grant needs the phone to be paired again, or a renewal with the same capabilities, signed by the device within the old
grant's life.

**Capabilities, first build:** read what busmon's For you shows, and answer an ask. Nothing
else. No directive, no merge, no promotion, and nothing typed into a pane (§6 Q1).

**One action, one use.** To answer, the phone fetches a pending approval. It holds the hash of
the exact request: ask id, question id, chosen option, and a hash of the ask's definition. It is
valid for at most 120 s. The phone signs that hash with its device key. The host checks the
signature, the hash against the ask as it stands now, and the 120 s window. It then spends the
approval's nonce with `SET NX` in Redis, so a replay fails. A changed or replaced ask voids the
pending approval.

**The answer it writes.** The host writes it through `Bus.Answer` (`bus/asks.go:464`), as the
person, with `typed=yes` and an additive `via=phone:<device>`. An agent and busmon then see
where it came from (§6 Q2).

**Transport.** First LAN only: the host's `agentbus web` listener and the phone talk Noise IK
(`flynn/noise`), with the host's static key pinned at pairing. Later, a relay may forward the
Noise traffic and see only ciphertext (§6 Q4). Once this is built, it replaces ntfy and Discord as
the channel for decisions. They stay as one-way bells.

## 3. Where it sits

The verifier is a host process, the `agentbus web` service, running as the person's user. It
is the only holder of the issuer key's use. It is not an agent. Agents never see a grant, a
device key or a pending approval. A grant whose capability list grows needs a new pairing step at
the desk: a grant is never widened remotely.

The limit is the same as for `typed=yes` today. On one host, any process of the same user that
can read `issuer.key` could sign. The deny, and idea 3's sandbox, keep the roles away from it.
The person's own shell is not confined.

## 4. Build steps (later, after the read-only web)

0. A spike: `flynn/noise` IK between a Go host and a phone client. The phone app is the open
   cost: a PWA with WebCrypto Ed25519, or a small native app.
1. Keys, pairing, grants, revocation, and `agentbus phone pair/list/revoke`.
2. Pending approvals, the hash binding, the nonce, and answers through `Bus.Answer` with `via`.
3. LAN transport. A relay is a separate spec.

## 5. Tests, and what this does not do

**Tests:** a grant past its expiry, with an older revision, from another device, or with a bad
signature is refused. A grant with `expires_at` more than 24 h after `issued_at` is never
signed. An approval older than 120 s, a replayed one, one whose ask changed since, and one for
another option are each refused. One valid answer writes exactly one reply, with `typed=yes` and
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

**Q3. Grant lifetime.**
- **(A) recommended: 24 h,** the note's bound.
- (B) 8 h.

**Q4. Away from home.**
- **(A) recommended: LAN only first,** with a relay as a later spec.
- (B) a relay in the first build.
